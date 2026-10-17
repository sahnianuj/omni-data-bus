import type { HopsOf } from '@/features/mapping/relationsApi'
import type { DocField, JunctionCandidate, MappingSpec, RelationGraph, RelationHop } from '@/features/mapping/types'
import type { TableRef } from './composerOps'
import { nodeOf, scopeOf, type ForeignKey } from './composerOps'

// What the Bind dialog offers when a table is dropped on a node (OC-048): the relations from
// the node's scope table to the dropped table in the relationship graph (OC-042) — foreign keys
// both ways, virtual relationships, and junctions for many-to-many — best fit first.

export type JoinPair = { parent: string; child: string }

export type BindOption =
  | { type: 'direct'; kind: 'many-to-one' | 'one-to-many'; table: TableRef; join: JoinPair[]; virtual?: boolean; constraint?: string }
  | { type: 'm2m'; table: TableRef; via: TableRef; parentJoin: JunctionCandidate['parentJoin']; childJoin: JunctionCandidate['childJoin']; payloadColumns: string[] }

export type Prefill = {
  /** The table whose rows the node's relation joins from (the driving table, or its scope relation's). */
  from: TableRef
  options: BindOption[]
  /** No relationship connects the scope to the table: offer to define one (OC-042). */
  noPath: boolean
}

const key = (t: TableRef) => `${t.schema}.${t.name}`
const same = (a: TableRef, b: TableRef) => a.schema === b.schema && a.name === b.name

/** The table a node's relation would join from. */
export function scopeTable(spec: MappingSpec, nodeId: string, own: TableRef): TableRef {
  const scope = scopeOf(spec, nodeId)
  if (!scope) return spec.root?.table ?? own
  return spec.relations.find((r) => r.id === scope)?.table ?? own
}

/** Every hop of the graph, once. */
export function edges(graph: RelationGraph | undefined): RelationHop[] {
  const out = new Map<string, RelationHop>()
  for (const p of graph?.paths ?? []) {
    for (const h of p.hops) {
      const k = `${key(h.from)}|${h.kind}|${key(h.to)}|${h.join.map((j) => `${j.parent}=${j.child}`).join(',')}`
      if (!out.has(k)) out.set(k, h)
    }
  }
  return [...out.values()]
}

/**
 * The options for binding a node to table, best fit first: an array prefers one-to-many, then
 * many-to-many, then many-to-one (wrapped); an object prefers many-to-one, then the first row of
 * a one-to-many or many-to-many.
 */
export function prefill(
  graph: RelationGraph | undefined,
  spec: MappingSpec,
  nodeId: string,
  table: TableRef,
  own: TableRef,
  scopeHops?: { hops?: RelationHop[]; junctions?: JunctionCandidate[] },
  /** The table joined from, when not the node's scope (a lookup at a bound node joins from its own table). */
  fromTable?: TableRef,
): Prefill {
  const from = fromTable ?? scopeTable(spec, nodeId, own)
  const options: BindOption[] = []
  // The scope table's own hops when they're known (OC-141, any depth), else the driving graph's.
  const hops = scopeHops?.hops ?? edges(graph)
  for (const h of hops) {
    if (same(h.from, from) && same(h.to, table)) {
      options.push({ type: 'direct', kind: h.kind as 'many-to-one' | 'one-to-many', table: h.to, join: h.join, virtual: h.virtual, constraint: h.constraint })
    }
  }
  const junctions = scopeHops?.junctions ?? (graph && same(graph.root, from) ? graph.junctions : [])
  for (const j of junctions) {
    if (same(j.table, table)) options.push({ type: 'm2m', table: j.table, via: j.via, parentJoin: j.parentJoin, childJoin: j.childJoin, payloadColumns: j.payloadColumns })
  }
  const isArray = nodeOf(spec, nodeId)?.type === 'array'
  const rank = (o: BindOption) => {
    const k = o.type === 'm2m' ? 'many-to-many' : o.kind
    return isArray ? { 'one-to-many': 0, 'many-to-many': 1, 'many-to-one': 2 }[k] : { 'many-to-one': 0, 'one-to-many': 1, 'many-to-many': 2 }[k]
  }
  options.sort((a, b) => rank(a) - rank(b))
  return { from, options, noPath: options.length === 0 }
}

/** The kind of relation an option creates. */
export const kindOf = (o: BindOption) => (o.type === 'm2m' ? 'many-to-many' : o.kind)

/**
 * The foreign key a field's column is on (OC-064, OC-143): the field is mapped from one column of
 * its own scope table, and a many-to-one hop leaves that table on that column, found among the
 * scope table's own hops (OC-141), so a key at any depth qualifies. A composite key qualifies
 * when its other columns are mapped in the same object too.
 */
export function foreignKeyOf(hopsOf: HopsOf, spec: MappingSpec, fieldId: string, own: TableRef): ForeignKey | undefined {
  const column = (id: string) => {
    const m = spec.fieldMappings.find((x) => x.targetFieldId === id && x.state === 'active')
    if (!m || m.sources.length !== 1 || m.transforms.some((t) => t.op === 'constant' || t.op === 'expression')) return undefined
    const src = m.sources[0]!
    return (src.relation ?? '') === scopeOf(spec, id) && !src.from ? src.column : undefined
  }
  const col = column(fieldId)
  if (!col) return undefined
  const scope = scopeOf(spec, fieldId)
  const siblings = new Set(
    (parentOf(spec.docSchema.root, fieldId)?.children ?? []).filter((c) => scopeOf(spec, c.id) === scope).map((c) => column(c.id)).filter((c): c is string => !!c),
  )
  const from = scopeTable(spec, fieldId, own)
  const hop = (hopsOf(from) ?? []).find(
    (h) => h.kind === 'many-to-one' && same(h.from, from) && h.join.some((j) => j.parent === col) && h.join.every((j) => siblings.has(j.parent)),
  )
  return hop ? { table: hop.to, join: hop.join, virtual: hop.virtual } : undefined
}

/** The object holding a field, if any. */
function parentOf(node: DocField, id: string): DocField | undefined {
  for (const c of node.children ?? []) {
    if (c.id === id) return node
    const inner = parentOf(c, id) ?? (c.items ? (c.items.id === id ? undefined : parentOf(c.items, id)) : undefined)
    if (inner) return inner
  }
  return undefined
}

/** How deep a field's scope relation sits (0 = the root, 1 = a relation of the root…). */
export function scopeDepth(spec: MappingSpec, fieldId: string): number {
  let id: string | null | undefined = scopeOf(spec, fieldId) || null
  let depth = 0
  while (id) {
    depth++
    id = spec.relations.find((r) => r.id === id)?.parentId ?? null
    if (depth > 16) break // a cycle: validation reports it
  }
  return depth
}
