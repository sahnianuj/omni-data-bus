import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, call } from '@/lib/api/client'
import type { JunctionCandidate, RelationGraph, RelationHop, RelationHops, Relationship, RelationshipDetection, RelationshipInput } from './types'

// The relationship graph and virtual relationships (OC-042).

export const relationKeys = {
  graph: (connId: string, root: string, depth: number) => ['relations', connId, root, depth] as const,
  hops: (connId: string, table: string) => ['relations', connId, 'hops', table] as const,
  graphs: (connId: string) => ['relations', connId] as const,
  virtual: (projectId: string) => ['relationships', projectId] as const,
}

/** Paths from root (schema.table), junction candidates and index advice. */
export function useRelationGraph(connId: string, root: string, depth = 4) {
  return useQuery({
    queryKey: relationKeys.graph(connId, root, depth),
    enabled: connId !== '' && root.includes('.'),
    retry: false,
    queryFn: (): Promise<RelationGraph> =>
      call(api.GET('/api/v1/connections/{id}/relations', { params: { path: { id: connId }, query: { root, depth } } })),
  })
}

const hopsQuery = (connId: string, table: string) => ({
  queryKey: relationKeys.hops(connId, table),
  enabled: connId !== '' && table.includes('.'),
  retry: false,
  staleTime: 60_000,
  queryFn: (): Promise<RelationHops> => call(api.GET('/api/v1/connections/{id}/relations/hops', { params: { path: { id: connId }, query: { table } } })),
})

/** Every direct relationship of one table (schema.table), cached per table (OC-141). */
export function useRelationHops(connId: string, table: string) {
  return useQuery(hopsQuery(connId, table))
}

/** A table's hops, or undefined while they load. */
export type HopsOf = (table: { schema: string; name: string }) => RelationHop[] | undefined

/**
 * The hops of several tables at once (one cached request per table, shared by every caller),
 * as a lookup, plus the relation depth limit once any answer arrived.
 */
export function useTablesHops(connId: string, tables: string[]): { hopsOf: HopsOf; junctionsOf: (table: { schema: string; name: string }) => JunctionCandidate[] | undefined; maxRelationDepth?: number } {
  const unique = [...new Set(tables)].sort()
  const results = useQueries({ queries: unique.map((t) => hopsQuery(connId, t)) })
  const byTable = new Map<string, RelationHops>()
  unique.forEach((t, i) => {
    const d = results[i]?.data
    if (d) byTable.set(t, d)
  })
  return {
    hopsOf: (t) => byTable.get(`${t.schema}.${t.name}`)?.hops,
    junctionsOf: (t) => byTable.get(`${t.schema}.${t.name}`)?.junctions,
    maxRelationDepth: [...byTable.values()][0]?.maxRelationDepth,
  }
}

/** The project's virtual relationships. */
export function useRelationships(projectId: string) {
  return useQuery({
    queryKey: relationKeys.virtual(projectId),
    retry: false,
    queryFn: async (): Promise<Relationship[]> => (await call(api.GET('/api/v1/projects/{id}/relationships', { params: { path: { id: projectId } } }))).items,
  })
}

function useInvalidate(projectId: string) {
  const qc = useQueryClient()
  return () => {
    void qc.invalidateQueries({ queryKey: relationKeys.virtual(projectId) })
    void qc.invalidateQueries({ queryKey: relationKeys.graphs(`${projectId}::source`) })
  }
}

export function useCreateRelationship(projectId: string) {
  const invalidate = useInvalidate(projectId)
  return useMutation({
    mutationFn: (body: RelationshipInput) => call(api.POST('/api/v1/projects/{id}/relationships', { params: { path: { id: projectId } }, body })),
    onSuccess: invalidate,
  })
}

export function useDeleteRelationship(projectId: string) {
  const invalidate = useInvalidate(projectId)
  return useMutation({
    mutationFn: (rid: string) => call(api.DELETE('/api/v1/projects/{id}/relationships/{rid}', { params: { path: { id: projectId, rid } } })),
    onSuccess: invalidate,
  })
}

/** Samples the source to suggest a cardinality (10 s; "unknown" when it times out). */
export function detectCardinality(projectId: string, body: RelationshipInput): Promise<RelationshipDetection> {
  return call(api.POST('/api/v1/projects/{id}/relationships/detect', { params: { path: { id: projectId } }, body }))
}
