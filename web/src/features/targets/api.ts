import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, call } from '@/lib/api/client'
import type { components } from '@/lib/api/schema'
import type { BucketSpec } from './names'

export type BucketList = components['schemas']['BucketList']
export type BucketInfo = components['schemas']['BucketInfo']
export type ScopeInfo = components['schemas']['ScopeInfo']
export type CollectionInfo = components['schemas']['CollectionInfo']
export type TargetCapabilities = components['schemas']['TargetCapabilities']

export const keyspaceKeys = {
  all: (connId: string) => ['keyspaces', connId] as const,
  buckets: (connId: string) => ['keyspaces', connId, 'buckets'] as const,
  scopes: (connId: string, bucket: string) => ['keyspaces', connId, 'scopes', bucket] as const,
  collection: (connId: string, bucket: string, scope: string, coll: string) => ['keyspaces', connId, 'collection', bucket, scope, coll] as const,
}

export function useBuckets(connId: string) {
  return useQuery({
    queryKey: keyspaceKeys.buckets(connId),
    enabled: connId !== '',
    retry: false,
    queryFn: () => call(api.GET('/api/v1/connections/{id}/buckets', { params: { path: { id: connId } } })),
  })
}

export function useScopes(connId: string, bucket: string) {
  return useQuery({
    queryKey: keyspaceKeys.scopes(connId, bucket),
    enabled: connId !== '' && bucket !== '',
    retry: false,
    queryFn: async () => (await call(api.GET('/api/v1/connections/{id}/buckets/{bucket}/scopes', { params: { path: { id: connId, bucket } } }))).scopes,
  })
}

/** One collection with its (lazy, best-effort) document count. */
export function useCollection(connId: string, bucket: string, scope: string, collection: string, enabled = true) {
  return useQuery({
    queryKey: keyspaceKeys.collection(connId, bucket, scope, collection),
    enabled: enabled && connId !== '' && bucket !== '' && scope !== '' && collection !== '',
    retry: false,
    queryFn: () =>
      call(
        api.GET('/api/v1/connections/{id}/buckets/{bucket}/scopes/{scope}/collections/{collection}', {
          params: { path: { id: connId, bucket, scope, collection } },
        }),
      ),
  })
}

/** How a create ended: newly created, still becoming available, or it already existed (selected instead). */
export type CreateOutcome = 'created' | 'pending' | 'exists'

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))
export const pendingPoll = { ms: 1000, attempts: 30 }

type Target = { bucket: string; scope?: string; collection?: string }

/** Creates a bucket, scope or collection. A 409 means "select the existing one"; a 202 is polled until usable. */
export function useCreateKeyspace(connId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (req: ({ kind: 'bucket' } & BucketSpec) | { kind: 'scope'; bucket: string; name: string } | { kind: 'collection'; bucket: string; scope: string; name: string; maxExpirySec: number }): Promise<{ outcome: CreateOutcome } & Target> => {
      const path = { id: connId }
      try {
        let target: Target
        let status: 'ready' | 'pending'
        if (req.kind === 'bucket') {
          const body = {
            name: req.name,
            bucketType: req.bucketType,
            ramQuotaMB: req.ramQuotaMB,
            numReplicas: req.numReplicas,
            storageBackend: req.bucketType === 'couchbase' ? (req.storageBackend as 'couchstore' | 'magma') : undefined,
          }
          ;({ status } = await call(api.POST('/api/v1/connections/{id}/buckets', { params: { path }, body })))
          target = { bucket: req.name }
        } else if (req.kind === 'scope') {
          ;({ status } = await call(api.POST('/api/v1/connections/{id}/buckets/{bucket}/scopes', { params: { path: { ...path, bucket: req.bucket } }, body: { name: req.name } })))
          target = { bucket: req.bucket, scope: req.name }
        } else {
          ;({ status } = await call(
            api.POST('/api/v1/connections/{id}/buckets/{bucket}/scopes/{scope}/collections', {
              params: { path: { ...path, bucket: req.bucket, scope: req.scope } },
              body: { name: req.name, maxExpirySec: req.maxExpirySec },
            }),
          ))
          target = { bucket: req.bucket, scope: req.scope, collection: req.name }
          if (status === 'pending') {
            // The cluster is still distributing the collection: poll until it answers.
            for (let i = 0; i < pendingPoll.attempts; i++) {
              await sleep(pendingPoll.ms)
              try {
                await call(api.GET('/api/v1/connections/{id}/buckets/{bucket}/scopes/{scope}/collections/{collection}', { params: { path: { id: connId, bucket: req.bucket, scope: req.scope, collection: req.name } } }))
                status = 'ready'
                break
              } catch {
                /* not there yet */
              }
            }
          }
        }
        return { outcome: status === 'ready' ? 'created' : 'pending', ...target }
      } catch (e) {
        if (e instanceof ApiError && e.code === 'KEYSPACE_EXISTS') {
          const target = req.kind === 'bucket' ? { bucket: req.name } : req.kind === 'scope' ? { bucket: req.bucket, scope: req.name } : { bucket: req.bucket, scope: req.scope, collection: req.name }
          return { outcome: 'exists', ...target }
        }
        throw e
      }
    },
    onSettled: () => qc.invalidateQueries({ queryKey: keyspaceKeys.all(connId) }),
  })
}
