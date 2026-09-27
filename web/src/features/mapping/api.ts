import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { projectKeys } from '@/features/projects/api'
import { api, call } from '@/lib/api/client'
import type { components } from '@/lib/api/schema'
import type { DocSchema, MappingSpec, MappingTarget, MappingView } from './types'

export type CreateMappingRequest = components['schemas']['CreateMappingRequest']
export type MappingPreviewRequest = components['schemas']['MappingPreviewRequest']
export type InferResult = components['schemas']['InferResult']

export const mappingKeys = {
  list: (projectId: string) => ['mappings', projectId] as const,
  one: (projectId: string, id: string) => ['mappings', projectId, id] as const,
}

export function useMappings(projectId: string) {
  return useQuery({
    queryKey: mappingKeys.list(projectId),
    queryFn: async () => (await call(api.GET('/api/v1/projects/{id}/mappings', { params: { path: { id: projectId } } }))).items,
  })
}

export function useMapping(projectId: string, id: string) {
  return useQuery({
    queryKey: mappingKeys.one(projectId, id),
    staleTime: Infinity, // the workbench owns the live copy; refetch only on reload
    queryFn: () => call(api.GET('/api/v1/projects/{id}/mappings/{mid}', { params: { path: { id: projectId, mid: id } } })),
  })
}

function useInvalidate(projectId: string) {
  const qc = useQueryClient()
  return () => {
    void qc.invalidateQueries({ queryKey: mappingKeys.list(projectId) })
    void qc.invalidateQueries({ queryKey: projectKeys.one(projectId) })
    void qc.invalidateQueries({ queryKey: projectKeys.list() })
  }
}

export function useBulkCreateMappings(projectId: string) {
  const invalidate = useInvalidate(projectId)
  return useMutation({
    mutationFn: async (items: CreateMappingRequest[]) =>
      (await call(api.POST('/api/v1/projects/{id}/mappings:bulk', { params: { path: { id: projectId } }, body: { items } }))).results,
    onSettled: invalidate,
  })
}

export function useCreateMapping(projectId: string) {
  const invalidate = useInvalidate(projectId)
  return useMutation({
    mutationFn: (body: CreateMappingRequest) => call(api.POST('/api/v1/projects/{id}/mappings', { params: { path: { id: projectId } }, body })),
    onSettled: invalidate,
  })
}

export function useDeleteMapping(projectId: string) {
  const invalidate = useInvalidate(projectId)
  return useMutation({
    mutationFn: (id: string) => call(api.DELETE('/api/v1/projects/{id}/mappings/{mid}', { params: { path: { id: projectId, mid: id } } })),
    onSettled: invalidate,
  })
}

/** PUT with If-Match; a stale version is a 412 MAPPING_VERSION_CONFLICT ApiError. */
export function saveMapping(projectId: string, id: string, version: number, spec: MappingSpec, target?: MappingTarget): Promise<MappingView> {
  return call(
    api.PUT('/api/v1/projects/{id}/mappings/{mid}', {
      params: { path: { id: projectId, mid: id }, header: { 'If-Match': `"${version}"` } },
      body: { spec, ...(target ? { target } : {}) },
    }),
  )
}

export function fetchMapping(projectId: string, id: string): Promise<MappingView> {
  return call(api.GET('/api/v1/projects/{id}/mappings/{mid}', { params: { path: { id: projectId, mid: id } } }))
}

export function runAutomap(projectId: string, id: string, version: number, mode: 'fill' | 'reset'): Promise<MappingView> {
  return call(
    api.POST('/api/v1/projects/{id}/mappings/{mid}/automap', {
      params: { path: { id: projectId, mid: id }, header: { 'If-Match': `"${version}"` } },
      body: { mode },
    }),
  )
}

export type AddEmbedRequest = components['schemas']['AddEmbedRequest']

/** Embeds a related table (OC-021), generating and auto-mapping its fields when asked. */
export function addEmbed(projectId: string, id: string, version: number, body: AddEmbedRequest): Promise<MappingView> {
  return call(
    api.POST('/api/v1/projects/{id}/mappings/{mid}/embeds', {
      params: { path: { id: projectId, mid: id }, header: { 'If-Match': `"${version}"` } },
      body,
    }),
  )
}

export function replaceDesign(projectId: string, id: string, version: number, docSchema: DocSchema, strategy: 'replace' | 'merge') {
  return call(
    api.POST('/api/v1/projects/{id}/mappings/{mid}/design', {
      params: { path: { id: projectId, mid: id }, header: { 'If-Match': `"${version}"` } },
      body: { docSchema, strategy },
    }),
  )
}

export function validateMapping(projectId: string, id: string) {
  return call(api.POST('/api/v1/projects/{id}/mappings/{mid}/validate', { params: { path: { id: projectId, mid: id } } }))
}

export function previewMapping(projectId: string, id: string, body: MappingPreviewRequest) {
  return call(api.POST('/api/v1/projects/{id}/mappings/{mid}/preview', { params: { path: { id: projectId, mid: id } }, body }))
}

export function inferDocSchema(kind: 'json-sample' | 'json-schema', content: string): Promise<InferResult> {
  return call(api.POST('/api/v1/doc-schemas/infer', { body: { kind, content } }))
}

export function inferFromCollection(connId: string, target: MappingTarget): Promise<InferResult> {
  return call(
    api.POST('/api/v1/connections/{id}/buckets/{bucket}/scopes/{scope}/collections/{collection}/infer-schema', {
      params: { path: { id: connId, bucket: target.bucket, scope: target.scope, collection: target.collection } },
    }),
  )
}

/** The design as JSON Schema text (through the API client, so the CSRF header is sent). */
export async function exportJsonSchema(docSchema: DocSchema): Promise<string> {
  const { data, response } = await api.POST('/api/v1/doc-schemas/export', { body: { docSchema }, parseAs: 'text' })
  if (!response.ok || typeof data !== 'string') throw new Error(`Export failed (${response.status})`)
  return data
}
