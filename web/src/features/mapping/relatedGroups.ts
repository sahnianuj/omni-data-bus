import type { EmbedCandidate, RelationHop } from './types'

export type Direction = 'references' | 'referencedBy'
export type Cardinality = 'one-to-one' | 'many-to-one' | 'one-to-many' | 'many-to-many'

/**
 * Which way a hop points and its cardinality (OC-157), as the server sends them; a response
 * without them (older server or cache) falls back to the kind: many-to-one is a table this one
 * references, one-to-many a table referencing it.
 */
export function directionOf(h: Pick<RelationHop | EmbedCandidate, 'kind' | 'direction' | 'cardinality'>): { direction: Direction; cardinality: Exclude<Cardinality, 'many-to-many'> } {
  const fromKind = h.kind === 'one-to-many' ? 'referencedBy' : 'references'
  return { direction: h.direction ?? fromKind, cardinality: h.cardinality ?? (h.kind === 'one-to-many' ? 'one-to-many' : 'many-to-one') }
}

/** The chip on a related-table row (1:1, N:1, 1:N, M:N) and the i18n suffix of its label. */
export const cardinalityChip: Record<Cardinality, { text: string; key: string }> = {
  'one-to-one': { text: '1:1', key: 'oneToOne' },
  'many-to-one': { text: 'N:1', key: 'manyToOne' },
  'one-to-many': { text: '1:N', key: 'oneToMany' },
  'many-to-many': { text: 'M:N', key: 'manyToMany' },
}

/**
 * Splits related-table rows into References (tables this one points to) and Referenced by
 * (tables pointing to it), keeping their order (OC-157).
 */
export function groupByDirection<T extends { hop: { direction: Direction } }>(nodes: T[]): { references: T[]; referencedBy: T[] } {
  const references: T[] = []
  const referencedBy: T[] = []
  for (const n of nodes) (n.hop.direction === 'references' ? references : referencedBy).push(n)
  return { references, referencedBy }
}
