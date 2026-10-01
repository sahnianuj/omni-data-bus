import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, call } from '@/lib/api/client'
import type { RelationGraph, Relationship, RelationshipDetection, RelationshipInput } from './types'

// The relationship graph and virtual relationships (OC-042).

export const relationKeys = {
  graph: (connId: string, root: string, depth: number) => ['relations', connId, root, depth] as const,
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
