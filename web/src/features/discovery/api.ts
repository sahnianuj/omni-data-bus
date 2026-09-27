import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, call } from '@/lib/api/client'
import type { components } from '@/lib/api/schema'

export type SourceTable = components['schemas']['SourceTable']
export type SourceColumn = components['schemas']['SourceColumn']
export type TableKind = components['schemas']['TableKind']
export type JsonType = components['schemas']['JsonType']
export type DiscoveryStatus = components['schemas']['DiscoveryStatus']
export type SchemaDiff = components['schemas']['SchemaDiff']
export type PreviewResult = components['schemas']['PreviewResult']
export type PreviewCell = components['schemas']['PreviewCell']

/** The latest snapshot with full table detail (the explorer needs columns for its tree and search). */
export type Snapshot = Omit<components['schemas']['SchemaResponse'], 'schemas'> & {
  schemas: { name: string; tables: SourceTable[] }[]
}

export const discoveryKeys = {
  all: (connId: string) => ['discovery', connId] as const,
  schema: (connId: string) => ['discovery', connId, 'schema'] as const,
  preview: (connId: string, schema: string, table: string, limit: number) => ['discovery', connId, 'preview', schema, table, limit] as const,
}

/** The latest discovered schema, or null when the repository hasn't been discovered yet. */
export function useSchema(connId: string) {
  return useQuery({
    queryKey: discoveryKeys.schema(connId),
    enabled: connId !== '',
    retry: false,
    queryFn: async (): Promise<Snapshot | null> => {
      try {
        return (await call(api.GET('/api/v1/connections/{id}/schema', { params: { path: { id: connId } } }))) as Snapshot
      } catch (e) {
        if (e instanceof ApiError && e.code === 'SCHEMA_NOT_DISCOVERED') return null
        throw e
      }
    },
  })
}

/** One table's full detail (columns, keys) from the latest snapshot. */
export function useSourceTable(connId: string, schema: string, table: string) {
  return useQuery({
    queryKey: [...discoveryKeys.all(connId), 'table', schema, table] as const,
    enabled: connId !== '' && table !== '',
    queryFn: () => call(api.GET('/api/v1/connections/{id}/schema/{schema}/{table}', { params: { path: { id: connId, schema, table } } })),
  })
}

export function usePreview(connId: string, schema: string, table: string, limit: number, enabled: boolean) {
  return useQuery({
    queryKey: discoveryKeys.preview(connId, schema, table, limit),
    enabled,
    staleTime: 0,
    gcTime: 0,
    retry: false,
    placeholderData: keepPreviousData,
    queryFn: () =>
      call(
        api.GET('/api/v1/connections/{id}/tables/{schema}/{table}/preview', {
          params: { path: { id: connId, schema, table }, query: { limit } },
        }),
      ),
  })
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

/** How often a running (202) discovery is polled. Exported so tests can shorten it. */
export const pollInterval = { ms: 1000 }

/** Runs discovery; a 202 (large catalog, or a run already in flight) is polled until it finishes. */
export function useDiscover(connId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (): Promise<DiscoveryStatus> => {
      let status = await call(api.POST('/api/v1/connections/{id}/discover', { params: { path: { id: connId } } }))
      while (status.status === 'running') {
        await sleep(pollInterval.ms)
        status = await call(
          api.GET('/api/v1/connections/{id}/discoveries/{did}', { params: { path: { id: connId, did: status.discoveryId } } }),
        )
      }
      if (status.status === 'failed') {
        const p = status.error
        throw new ApiError(p?.status ?? 500, p ?? { title: 'Discovery failed' })
      }
      return status
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: discoveryKeys.all(connId) }),
  })
}
