import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { projectKeys } from '@/features/projects/api'
import { stripJsonc } from '@/features/docschema/stripJsonc'
import { api, call } from '@/lib/api/client'
import type { components } from '@/lib/api/schema'
import type { DocSchema, MappingSpec, MappingTarget, MappingView } from './types'
import { upgradeView } from './upgradeSpec'

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
    queryFn: () => fetchMapping(projectId, id),
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

/**
 * Moves mappings to another bucket and scope, keeping each collection name (created when the
 * migration starts if it doesn't exist). One save per mapping, in order; stops at the first error.
 */
export function useMoveMappings(projectId: string) {
  const invalidate = useInvalidate(projectId)
  return useMutation({
    mutationFn: async ({ ids, bucket, scope }: { ids: string[]; bucket: string; scope: string }) => {
      for (const id of ids) {
        const view = await fetchMapping(projectId, id)
        await saveMapping(projectId, id, view.version, view.spec, { ...view.target, bucket, scope, createIfMissing: true })
      }
    },
    onSettled: invalidate,
  })
}

/** PUT with If-Match; a stale version is a 412 MAPPING_VERSION_CONFLICT ApiError. */
export function saveMapping(projectId: string, id: string, version: number, spec: MappingSpec, target?: MappingTarget): Promise<MappingView> {
  return v2(
    api.PUT('/api/v1/projects/{id}/mappings/{mid}', {
      params: { path: { id: projectId, mid: id }, header: { 'If-Match': `"${version}"` } },
      body: { spec, ...(target ? { target } : {}) },
    }),
  )
}

export function fetchMapping(projectId: string, id: string): Promise<MappingView> {
  return v2(api.GET('/api/v1/projects/{id}/mappings/{mid}', { params: { path: { id: projectId, mid: id } } }))
}

/** Calls the API and upgrades the returned mapping's spec to v2 (a cached 1.0 response, OC-037 AC5). */
function v2(req: Parameters<typeof call<MappingView>>[0]): Promise<MappingView> {
  return call(req).then(upgradeView)
}

export function runAutomap(projectId: string, id: string, version: number, mode: 'fill' | 'reset'): Promise<MappingView> {
  return v2(
    api.POST('/api/v1/projects/{id}/mappings/{mid}/automap', {
      params: { path: { id: projectId, mid: id }, header: { 'If-Match': `"${version}"` } },
      body: { mode },
    }),
  )
}

/** Auto-maps one bound scope of a composed document, or all of them from the root down (OC-049). */
export function runAutomapScope(projectId: string, id: string, version: number, body: { nodeId: string } | { allScopes: true }): Promise<MappingView> {
  return v2(
    api.POST('/api/v1/projects/{id}/mappings/{mid}/automap', {
      params: { path: { id: projectId, mid: id }, header: { 'If-Match': `"${version}"` } },
      body: { mode: 'fill', ...body },
    }),
  )
}

export type AddEmbedRequest = components['schemas']['AddEmbedRequest']

/** Embeds a related table (OC-021), generating and auto-mapping its fields when asked. */
export function addEmbed(projectId: string, id: string, version: number, body: AddEmbedRequest): Promise<MappingView> {
  return v2(
    api.POST('/api/v1/projects/{id}/mappings/{mid}/embeds', {
      params: { path: { id: projectId, mid: id }, header: { 'If-Match': `"${version}"` } },
      body,
    }),
  )
}

export function replaceDesign(projectId: string, id: string, version: number, docSchema: DocSchema, strategy: 'replace' | 'merge' | 'reconcile') {
  return call(
    api.POST('/api/v1/projects/{id}/mappings/{mid}/design', {
      params: { path: { id: projectId, mid: id }, header: { 'If-Match': `"${version}"` } },
      body: { docSchema, strategy },
    }),
  )
}

export type DesignDiff = components['schemas']['DesignDiff']
export type ReconcileResult = components['schemas']['ReconcileResult']

/** A revised design with the current field ids kept where fields still exist, and the diff (OC-061). */
export function reconcileDesign(current: DocSchema, incoming: DocSchema, declined: string[] = []): Promise<ReconcileResult> {
  return call(api.POST('/api/v1/doc-schemas/reconcile', { body: { current, incoming, ...(declined.length ? { declined } : {}) } }))
}

/** The mapping sheet (OC-061) as text, through the API client (so the session is sent). */
export async function mappingSheet(projectId: string, id: string, format: 'csv' | 'md'): Promise<{ text: string; fileName: string }> {
  const { data, response } = await api.GET('/api/v1/projects/{id}/mappings/{mid}/mapping-sheet', { params: { path: { id: projectId, mid: id }, query: { format } }, parseAs: 'text' })
  if (!response.ok || typeof data !== 'string') throw new Error(`Export failed (${response.status})`)
  const fileName = /filename="([^"]+)"/.exec(response.headers.get('Content-Disposition') ?? '')?.[1] ?? `mapping-sheet.${format}`
  return { text: data, fileName }
}

export function validateMapping(projectId: string, id: string) {
  return call(api.POST('/api/v1/projects/{id}/mappings/{mid}/validate', { params: { path: { id: projectId, mid: id } } }))
}

export function previewMapping(projectId: string, id: string, body: MappingPreviewRequest) {
  return call(api.POST('/api/v1/projects/{id}/mappings/{mid}/preview', { params: { path: { id: projectId, mid: id } }, body }))
}

export type BindingSuggestions = components['schemas']['BindingSuggestions']
export type BindingSuggestion = components['schemas']['BindingSuggestion']
export type BindingCandidate = components['schemas']['BindingCandidate']

/** Proposed driving table and bindings for unbound objects and arrays (OC-060); changes nothing. */
export function suggestBindings(projectId: string, id: string, body: { nodeId?: string; root?: boolean; specOverride?: MappingSpec }): Promise<BindingSuggestions> {
  return call(api.POST('/api/v1/projects/{id}/mappings/{mid}/suggest-bindings', { params: { path: { id: projectId, mid: id } }, body }))
}

export type Proposal = components['schemas']['Proposal']
export type ProposalItem = components['schemas']['ProposalItem']

/** The whole mapping of the design, proposed against the connected source (OC-163); changes nothing. */
export function proposeMapping(projectId: string, id: string, body: { specOverride?: MappingSpec; drivingTable?: string; base?: boolean; nodeId?: string }): Promise<Proposal> {
  return call(api.POST('/api/v1/projects/{id}/mappings/{mid}/propose', { params: { path: { id: projectId, mid: id } }, body }))
}

/** A mapping proposed for a design that has no mapping yet: New mapping from a design (OC-163). */
export function proposeDesign(projectId: string, docSchema: DocSchema, drivingTable?: string): Promise<Proposal> {
  return call(api.POST('/api/v1/projects/{id}/mappings/propose', { params: { path: { id: projectId } }, body: { docSchema, ...(drivingTable ? { drivingTable } : {}) } }))
}

/** files: a JSON Schema's other files, for designs split across files (OC-171). JSONC is sent as plain JSON. */
export function inferDocSchema(kind: 'json-sample' | 'json-schema', content: string, fileName?: string, files?: { name: string; content: string }[]): Promise<InferResult> {
  return call(api.POST('/api/v1/doc-schemas/infer', { body: { kind, content: stripJsonc(content), ...(fileName ? { fileName } : {}), ...(files?.length ? { files: files.map((f) => ({ ...f, content: stripJsonc(f.content) })) } : {}) } }))
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
