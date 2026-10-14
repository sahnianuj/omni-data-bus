import { leaves } from '@/features/docschema/docschemaOps'
import { boundRelations, fieldScopes } from '@/features/composer/scope'
import { srcKey } from './embeds'
import type { DocField, MappingSpec, MappingStats, SourceRef } from './types'

export type NodeScore = NonNullable<MappingStats['byNode']>[number]

const scalar = (t: DocField['type']) => t !== 'object' && t !== 'array'

/** Relation ids parents first, then by id (mapping.Depths); a cycle or a missing parent sorts last. */
function relationOrder(spec: MappingSpec): string[] {
  const byId = new Map(spec.relations.map((r) => [r.id, r]))
  const depth = (id: string, seen = new Set<string>()): number => {
    const p = byId.get(id)?.parentId
    if (!p) return 1
    if (seen.has(id) || !byId.has(p)) return Infinity
    seen.add(id)
    return depth(p, seen) + 1
  }
  return [...byId.keys()].map((id) => [id, depth(id)] as const).sort((a, b) => a[1] - b[1] || (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0)).map(([id]) => id)
}

/**
 * The score per bound scope, root first (OC-049 AC5), mirroring mapping.ByNode: each scope's
 * single-value fields and unbound arrays of values, and how many have an active mapping or aggregate.
 */
export function byNode(spec: MappingSpec): NodeScore[] {
  const scopes = fieldScopes(spec)
  const bound = boundRelations(spec)
  const mapped = new Set([...spec.fieldMappings.filter((m) => m.state === 'active').map((m) => m.targetFieldId), ...(spec.aggregates ?? []).map((a) => a.targetFieldId)])
  const paths = new Map<string, string>()
  const walk = (f: DocField, prefix: string) => {
    for (const c of f.children ?? []) {
      const p = prefix ? `${prefix}.${c.name}` : c.name
      paths.set(c.id, p)
      walk(c, p)
      if (c.items) walk(c.items, p)
    }
  }
  walk(spec.docSchema.root, '')
  const out: NodeScore[] = [{ nodeId: 'fld_root', path: 'root', mapped: 0, total: 0 }]
  const at = new Map<string, number>([['', 0]])
  for (const id of relationOrder(spec)) {
    const r = spec.relations.find((x) => x.id === id)!
    if (r.lookupOnly || !r.targetFieldId || !paths.has(r.targetFieldId)) continue
    at.set(id, out.length)
    out.push({ nodeId: r.targetFieldId, relationId: id, path: paths.get(r.targetFieldId)!, mapped: 0, total: 0 })
  }
  const visit = (f: DocField) => {
    for (const c of f.children ?? []) {
      const leaf = scalar(c.type) || (c.type === 'array' && !bound.has(c.id) && (!c.items || scalar(c.items.type)))
      const i = at.get(scopes.get(c.id) ?? '')
      if (leaf && i !== undefined) {
        out[i]!.total++
        if (mapped.has(c.id)) out[i]!.mapped++
      }
      visit(c)
      if (c.items) visit(c.items)
    }
  }
  visit(spec.docSchema.root)
  return out
}

/**
 * Live mapping score; mirrors mapping.ComputeStatsWith (parity: computeStats.test.ts). Columns
 * of embedded tables (embed id → names) are counted in the separate embedded group.
 */
export function computeStats(spec: MappingSpec, columns: string[], embedded?: Record<string, string[]>): MappingStats {
  const auto = new Set<string>()
  const manual = new Set<string>()
  const suggested = new Set<string>()
  const mappedTargets = new Set<string>()
  let confSum = 0
  let confN = 0
  // A grouped document's rows come from its own table (OC-063): columns read through the group
  // relation are the table's own, not an embedded table's.
  const group = spec.relations.find((r) => r.group)?.id
  const own = (s: SourceRef) => srcKey(group && s.relation === group ? { ...s, relation: undefined } : s)
  for (const m of spec.fieldMappings) {
    if (m.state === 'active') {
      mappedTargets.add(m.targetFieldId)
      for (const s of m.sources) (m.origin === 'auto' ? auto : manual).add(own(s))
      if (m.origin === 'auto') {
        confSum += m.confidence ?? 0
        confN++
      }
    } else if (m.state === 'pending') {
      for (const s of m.sources) suggested.add(own(s))
    }
  }
  const ignored = new Set(spec.ignoredColumns)
  const st: MappingStats = {
    totalSourceColumns: 0, autoMapped: 0, suggested: 0, manuallyMapped: 0, unmappedSource: 0, ignored: 0,
    totalTargetLeaves: 0, mappedTargetLeaves: 0, requiredTargetUnmapped: 0, autoMapRatio: 0, coverageRatio: 0, averageConfidence: 0,
  }
  const mapped = (k: string) => manual.has(k) || auto.has(k) || suggested.has(k)
  // The parent's join columns of its many-to-one relations: an unmapped one is carried by the object.
  const carried = new Set(spec.relations.filter((e) => e.kind === 'many-to-one').flatMap((e) => (e.join ?? []).map((j) => j.parent)))
  for (const c of columns) {
    if (ignored.has(c)) {
      st.ignored++
      continue
    }
    if (carried.has(c) && !mapped(c)) continue // an unmapped foreign key whose row is embedded
    st.totalSourceColumns++
    if (manual.has(c)) st.manuallyMapped++
    else if (auto.has(c)) st.autoMapped++
    else if (suggested.has(c)) st.suggested++
    else st.unmappedSource++
  }
  for (const l of leaves(spec.docSchema)) {
    st.totalTargetLeaves++
    if (mappedTargets.has(l.fieldId)) st.mappedTargetLeaves++
    else if (l.required) st.requiredTargetUnmapped++
  }
  if (st.totalSourceColumns > 0) {
    st.autoMapRatio = st.autoMapped / st.totalSourceColumns
    st.coverageRatio = (st.autoMapped + st.manuallyMapped) / st.totalSourceColumns
  }
  if (confN > 0) st.averageConfidence = confSum / confN
  if (spec.relations.length > 0 && embedded) {
    const e = { tables: 0, totalColumns: 0, autoMapped: 0, manuallyMapped: 0, suggested: 0, unmappedSource: 0 }
    for (const emb of spec.relations) {
      if (emb.id === group) continue // counted above, as the table's own columns
      e.tables++
      const joins = new Set((emb.join ?? []).map((j) => j.child))
      for (const c of embedded[emb.id] ?? []) {
        const k = srcKey({ column: c, relation: emb.id })
        if (joins.has(c) && !mapped(k)) continue // an unmapped join column is the relation itself, not data to map
        e.totalColumns++
        if (manual.has(k)) e.manuallyMapped++
        else if (auto.has(k)) e.autoMapped++
        else if (suggested.has(k)) e.suggested++
        else e.unmappedSource++
      }
    }
    st.embedded = e
  }
  if (spec.relations.length > 0) st.byNode = byNode(spec)
  return st
}
