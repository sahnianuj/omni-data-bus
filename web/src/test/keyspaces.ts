import { http, HttpResponse } from 'msw'
import type { BucketList, ScopeInfo, TargetCapabilities } from '@/features/targets/api'
import { server } from './msw'

export const caps = (over: Partial<TargetCapabilities> = {}): TargetCapabilities => ({
  canListBuckets: true,
  canCreateBucket: true,
  canManageScopes: true,
  canWriteDocs: true,
  rangeScan: true,
  queryService: true,
  deployment: 'self-managed',
  allBuckets: true,
  ...over,
})

export const bucketList = (over: Partial<BucketList> = {}): BucketList => ({
  buckets: [
    { name: 'travel-sample', type: 'couchbase', ramQuotaMB: 256, numReplicas: 1, storageBackend: 'couchstore', canManage: true },
    { name: 'archive', type: 'couchbase', ramQuotaMB: 1024, numReplicas: 0, storageBackend: 'magma', canManage: false },
  ],
  partial: false,
  capabilities: caps(),
  ...over,
})

export const travelScopes: ScopeInfo[] = [
  { name: '_default', collections: [{ name: '_default', maxExpirySec: 0 }] },
  { name: 'inventory', collections: [{ name: 'airline', maxExpirySec: 0 }, { name: 'route', maxExpirySec: 0 }] },
]

/** Serves buckets, scopes (travel-sample, archive) and collection counts. */
export function mockKeyspaces(list: BucketList = bucketList(), counts: Record<string, number | null> = {}) {
  server.use(
    http.get('*/api/v1/connections/:id/buckets', () => HttpResponse.json(list)),
    http.get('*/api/v1/connections/:id/buckets/:bucket/scopes', ({ params }) =>
      params.bucket === 'travel-sample'
        ? HttpResponse.json({ scopes: travelScopes })
        : params.bucket === 'archive'
          ? HttpResponse.json({ scopes: [{ name: '_default', collections: [{ name: '_default', maxExpirySec: 0 }] }] })
          : HttpResponse.json({ status: 404, code: 'NOT_FOUND', title: 'Bucket not found' }, { status: 404 }),
    ),
    http.get('*/api/v1/connections/:id/buckets/:bucket/scopes/:scope/collections/:collection', ({ params }) => {
      const n = counts[String(params.collection)]
      return HttpResponse.json({ name: params.collection, maxExpirySec: 0, ...(n != null ? { docCountEstimate: n } : {}) })
    }),
  )
}
