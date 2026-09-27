import Fuse, { type FuseResultMatch } from 'fuse.js'
import type { SourceColumn, SourceTable } from './api'

export type SchemaNode = { name: string; tables: SourceTable[] }

export type Range = [number, number]

export type TreeNode =
  | { kind: 'schema'; id: string; level: 1; name: string; count: number }
  | { kind: 'table'; id: string; level: 2; schema: string; name: string; table: SourceTable }
  | { kind: 'column'; id: string; level: 3; schema: string; table: string; name: string; column: SourceColumn; fk: boolean }

// Node IDs join names with a control character, so names containing dots can't collide.
const SEP = '\u0001'
export const schemaId = (schema: string) => `s${SEP}${schema}`
export const tableId = (schema: string, table: string) => `t${SEP}${schema}${SEP}${table}`
export const columnId = (schema: string, table: string, column: string) => `c${SEP}${schema}${SEP}${table}${SEP}${column}`

/** The parent node ID of a table or column node (undefined for schemas). */
export function parentId(node: TreeNode): string | undefined {
  if (node.kind === 'table') return schemaId(node.schema)
  if (node.kind === 'column') return tableId(node.schema, node.table)
  return undefined
}

export type Chip = 'tables' | 'views' | 'hasPk' | 'noPk'
export const chips: Chip[] = ['tables', 'views', 'hasPk', 'noPk']

const isView = (t: SourceTable) => t.kind === 'view' || t.kind === 'materialized_view'

/** Filter chips combine with AND. */
export function passesChips(t: SourceTable, active: ReadonlySet<Chip>): boolean {
  if (active.has('tables') && isView(t)) return false
  if (active.has('views') && !isView(t)) return false
  if (active.has('hasPk') && t.primaryKey.length === 0) return false
  if (active.has('noPk') && t.primaryKey.length > 0) return false
  return true
}

/** The result of a search: which tables and columns to show, what to expand, and highlight ranges per node. */
export type SearchResult = {
  tables: Set<string>
  /** Columns to show for tables that only matched through a column; other tables show all columns. */
  columns: Map<string, Set<string>>
  expand: Set<string>
  highlights: Map<string, Range[]>
}

type Entry = { path: string; id: string; labelOffset: number; kind: 'table' | 'column'; schema: string; table: string }

/** Builds the fuzzy index over "schema.table" and "schema.table.column". */
export function buildIndex(schemas: SchemaNode[]): Fuse<Entry> {
  const entries: Entry[] = []
  for (const s of schemas) {
    for (const t of s.tables) {
      entries.push({ path: `${s.name}.${t.name}`, id: tableId(s.name, t.name), labelOffset: s.name.length + 1, kind: 'table', schema: s.name, table: t.name })
      for (const c of t.columns) {
        entries.push({
          path: `${s.name}.${t.name}.${c.name}`,
          id: columnId(s.name, t.name, c.name),
          labelOffset: s.name.length + t.name.length + 2,
          kind: 'column',
          schema: s.name,
          table: t.name,
        })
      }
    }
  }
  // Extended search: space-separated terms must all match (fuzzily).
  return new Fuse(entries, { keys: ['path'], threshold: 0.3, ignoreLocation: true, includeMatches: true, useExtendedSearch: true })
}

function labelRanges(matches: readonly FuseResultMatch[] | undefined, offset: number): Range[] {
  const out: Range[] = []
  for (const m of matches ?? []) {
    for (const [a, b] of m.indices) {
      if (b < offset) continue
      out.push([Math.max(a, offset) - offset, b - offset])
    }
  }
  return out.sort((x, y) => x[0] - y[0])
}

export function search(index: Fuse<Entry>, query: string): SearchResult | null {
  const q = query.trim()
  if (!q) return null
  const res: SearchResult = { tables: new Set(), columns: new Map(), expand: new Set(), highlights: new Map() }
  const matchedTables = new Set<string>()
  for (const r of index.search(q)) {
    const e = r.item
    const tid = tableId(e.schema, e.table)
    res.tables.add(tid)
    res.expand.add(schemaId(e.schema))
    const ranges = labelRanges(r.matches, e.labelOffset)
    if (ranges.length) res.highlights.set(e.id, ranges)
    if (e.kind === 'table') {
      matchedTables.add(tid)
    } else if (ranges.length === 0) {
      // Only the schema/table part of the path matched: that's a match on the table itself.
      matchedTables.add(tid)
    } else {
      res.expand.add(tid)
      const cols = res.columns.get(tid) ?? new Set<string>()
      cols.add(e.id)
      res.columns.set(tid, cols)
    }
  }
  for (const tid of matchedTables) res.columns.delete(tid) // the table itself matched: show all its columns
  return res
}

/** Flattens the visible tree (for virtualization), honouring expansion, chips and search. */
export function flatten(schemas: SchemaNode[], expanded: ReadonlySet<string>, active: ReadonlySet<Chip>, result: SearchResult | null): TreeNode[] {
  const out: TreeNode[] = []
  for (const s of [...schemas].sort((a, b) => a.name.localeCompare(b.name))) {
    const tables = s.tables
      .filter((t) => passesChips(t, active) && (!result || result.tables.has(tableId(s.name, t.name))))
      .sort((a, b) => a.name.localeCompare(b.name))
    if (tables.length === 0 && (result || active.size > 0)) continue
    const sid = schemaId(s.name)
    out.push({ kind: 'schema', id: sid, level: 1, name: s.name, count: tables.length })
    if (!expanded.has(sid)) continue
    for (const t of tables) {
      const tid = tableId(s.name, t.name)
      out.push({ kind: 'table', id: tid, level: 2, schema: s.name, name: t.name, table: t })
      if (!expanded.has(tid)) continue
      const only = result?.columns.get(tid)
      const fkCols = new Set(t.foreignKeys.flatMap((f) => f.columns))
      for (const c of [...t.columns].sort((a, b) => a.ordinal - b.ordinal)) {
        const cid = columnId(s.name, t.name, c.name)
        if (only && !only.has(cid)) continue
        out.push({ kind: 'column', id: cid, level: 3, schema: s.name, table: t.name, name: c.name, column: c, fk: fkCols.has(c.name) })
      }
    }
  }
  return out
}

/** Whether a node can expand (schemas and tables with columns). */
export const expandable = (n: TreeNode) => n.kind === 'schema' || (n.kind === 'table' && n.table.columns.length > 0)
