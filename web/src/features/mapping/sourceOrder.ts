import { srcKey } from './embeds'
import { mappingReads } from './reads'
import type { CanvasColumn, DocField, Embed, MappingSpec } from './types'

/**
 * A source panel row: a column, the header of a related table's columns, or the divider before
 * a scope's unmapped columns (OC-175).
 */
export type SourceRow = { kind: 'column'; column: CanvasColumn } | { kind: 'embed'; embed: Embed } | { kind: 'divider'; scope: string }

/** Document: columns and groups in the order of the fields they fill. Table: creation order. */
export type SourceOrder = 'document' | 'table'

/** Every design field's position in the document, depth first (arrays' items included). */
export function designOrder(spec: MappingSpec): Map<string, number> {
  const out = new Map<string, number>()
  const visit = (f: DocField) => {
    out.set(f.id, out.size)
    for (const c of f.children ?? []) visit(c)
    if (f.items) visit(f.items)
  }
  visit(spec.docSchema.root)
  return out
}

/**
 * The source panel's rows. Table order is the table's columns, then each relation's group in
 * the order relations were made. Document order (OC-175) follows the design: a scope's mapped
 * columns and its relations' groups are merged by the position of the first field each fills
 * (a relation's own field; a lookup's or a total's first field that reads it), nested groups
 * right after their parent's; a scope's unmapped columns come last, after a divider. columns
 * are the canvas columns (the table's, then each relation's keyed "relation:column"); shown,
 * the visible ones; keepEmpty keeps a group with no visible column (no filter).
 */
export function orderSources(spec: MappingSpec, tableColumns: CanvasColumn[], columns: CanvasColumn[], shown: ReadonlySet<string>, keepEmpty: boolean, mode: SourceOrder): SourceRow[] {
  if (mode === 'table') {
    const rows: SourceRow[] = tableColumns.filter((c) => shown.has(c.name)).map((c) => ({ kind: 'column', column: c }))
    for (const e of spec.relations) {
      const cols = columns.filter((c) => c.embed === e.id && shown.has(c.name))
      if (cols.length === 0 && !keepEmpty) continue
      rows.push({ kind: 'embed', embed: e }, ...cols.map((c) => ({ kind: 'column' as const, column: c })))
    }
    return rows
  }

  const pos = designOrder(spec)
  const at = (fieldId: string | undefined) => (fieldId ? (pos.get(fieldId) ?? Infinity) : Infinity)
  // Where each column is read: the first field it fills (mappings, then totals).
  const colPos = new Map<string, number>()
  const read = (key: string, p: number) => colPos.set(key, Math.min(colPos.get(key) ?? Infinity, p))
  // Where each relation sits: its own field, else the first field that reads it.
  const relPos = new Map<string, number>()
  const touch = (rel: string | undefined, p: number) => {
    if (rel) relPos.set(rel, Math.min(relPos.get(rel) ?? Infinity, p))
  }
  // Its sources and the columns its expressions read (concat(row.first_name, …)).
  const reads = mappingReads(spec, new Set(columns.map((c) => c.name)))
  const relOf = new Map(columns.map((c) => [c.name, c.embed]))
  for (const m of spec.fieldMappings) {
    if (m.state !== 'active' && m.state !== 'pending') continue
    const p = at(m.targetFieldId)
    for (const s of m.sources) touch(s.relation, p)
    for (const k of reads(m)) {
      read(k, p)
      touch(relOf.get(k), p)
    }
  }
  for (const a of spec.aggregates ?? []) {
    const p = at(a.targetFieldId)
    if (a.column) read(srcKey({ column: a.column, relation: a.relationId, ...(a.from === 'via' ? { from: 'via' } : {}) }), p)
    touch(a.relationId, p)
  }
  for (const r of spec.relations) if (!r.lookupOnly && r.targetFieldId) relPos.set(r.id, at(r.targetFieldId))

  const children = new Map<string, Embed[]>()
  spec.relations.forEach((r) => {
    const parent = r.parentId ?? ''
    children.set(parent, [...(children.get(parent) ?? []), r])
  })
  const creation = new Map(spec.relations.map((r, i) => [r.id, i]))
  const ordinal = new Map(columns.map((c, i) => [c.name, i]))

  // A scope's rows: its mapped columns and its child groups by position, then its unmapped columns.
  const scope = (id: string, own: CanvasColumn[]): SourceRow[] => {
    type Item = { pos: number; tie: number; rows: SourceRow[] }
    const items: Item[] = []
    const unmapped: CanvasColumn[] = []
    for (const c of own) {
      if (!shown.has(c.name)) continue
      const p = colPos.get(c.name)
      if (p === undefined || p === Infinity) unmapped.push(c)
      else items.push({ pos: p, tie: ordinal.get(c.name) ?? 0, rows: [{ kind: 'column', column: c }] })
    }
    for (const r of children.get(id) ?? []) {
      const rows = group(r)
      if (rows.length) items.push({ pos: relPos.get(r.id) ?? Infinity, tie: columns.length + (creation.get(r.id) ?? 0), rows })
    }
    items.sort((a, b) => a.pos - b.pos || a.tie - b.tie)
    const out = items.flatMap((i) => i.rows)
    if (unmapped.length) {
      if (out.length) out.push({ kind: 'divider', scope: id })
      out.push(...unmapped.map((c) => ({ kind: 'column' as const, column: c })))
    }
    return out
  }
  const group = (r: Embed): SourceRow[] => {
    const inner = scope(
      r.id,
      columns.filter((c) => c.embed === r.id),
    )
    if (inner.length === 0 && !keepEmpty) return []
    return [{ kind: 'embed', embed: r }, ...inner]
  }
  return scope('', tableColumns)
}
