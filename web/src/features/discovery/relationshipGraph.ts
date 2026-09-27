import type { SourceTable } from './api'

export type GraphNode = { id: string; schema: string; table: string; role: 'self' | 'parent' | 'child' }
/** An FK edge from the referencing (child) table to the referenced (parent) table. */
export type GraphEdge = { from: string; to: string; name: string; columns: string[]; refColumns: string[] }

const nodeId = (schema: string, table: string) => `${schema}.${table}`

/**
 * The selected table with its FK parents (tables it references) and children (tables that
 * reference it). Neighbours appear once even with several FKs; a self-reference is an edge
 * from the table to itself.
 */
export function relationshipGraph(schema: string, t: SourceTable) {
  const self: GraphNode = { id: nodeId(schema, t.name), schema, table: t.name, role: 'self' }
  const parents = new Map<string, GraphNode>()
  const children = new Map<string, GraphNode>()
  const edges: GraphEdge[] = []
  for (const fk of t.foreignKeys) {
    const id = nodeId(fk.refSchema, fk.refTable)
    if (id !== self.id && !parents.has(id)) parents.set(id, { id, schema: fk.refSchema, table: fk.refTable, role: 'parent' })
    edges.push({ from: self.id, to: id, name: fk.name, columns: fk.columns, refColumns: fk.refColumns })
  }
  for (const ref of t.referencedBy) {
    const id = nodeId(ref.schema, ref.table)
    if (id === self.id) continue // already recorded from the FK side
    if (!children.has(id)) children.set(id, { id, schema: ref.schema, table: ref.table, role: 'child' })
    edges.push({ from: id, to: self.id, name: ref.name, columns: ref.columns, refColumns: ref.refColumns })
  }
  const byName = (a: GraphNode, b: GraphNode) => a.id.localeCompare(b.id)
  return {
    self,
    parents: [...parents.values()].sort(byName),
    children: [...children.values()].sort(byName),
    nodes: [self, ...[...parents.values()].sort(byName), ...[...children.values()].sort(byName)],
    edges,
  }
}
