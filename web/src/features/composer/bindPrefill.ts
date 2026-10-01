import type { JunctionCandidate, MappingSpec, RelationGraph, RelationHop } from '@/features/mapping/types'
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
export function prefill(graph: RelationGraph | undefined, spec: MappingSpec, nodeId: string, table: TableRef, own: TableRef): Prefill {
  const from = scopeTable(spec, nodeId, own)
  const options: BindOption[] = []
  for (const h of edges(graph)) {
    if (same(h.from, from) && same(h.to, table)) {
      options.push({ type: 'direct', kind: h.kind as 'many-to-one' | 'one-to-many', table: h.to, join: h.join, virtual: h.virtual, constraint: h.constraint })
    }
  }
  if (graph && same(graph.root, from)) {
    for (const j of graph.junctions) {
      if (same(j.table, table)) options.push({ type: 'm2m', table: j.table, via: j.via, parentJoin: j.parentJoin, childJoin: j.childJoin, payloadColumns: j.payloadColumns })
    }
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
 * The foreign key a field's column is on (OC-064): the field is mapped from one column of its
 * own scope table, and a many-to-one hop leaves that table on exactly that column.
 */
export function foreignKeyOf(graph: RelationGraph | undefined, spec: MappingSpec, fieldId: string, own: TableRef): ForeignKey | undefined {
  const m = spec.fieldMappings.find((x) => x.targetFieldId === fieldId && x.state === 'active')
  if (!m || m.sources.length !== 1 || m.transforms.some((t) => t.op === 'constant' || t.op === 'expression')) return undefined
  const src = m.sources[0]!
  if ((src.relation ?? '') !== scopeOf(spec, fieldId) || src.from) return undefined
  const from = scopeTable(spec, fieldId, own)
  const hop = edges(graph).find((h) => same(h.from, from) && h.kind === 'many-to-one' && h.join.length === 1 && h.join[0]!.parent === src.column)
  return hop ? { table: hop.to, join: hop.join, virtual: hop.virtual } : undefined
}
