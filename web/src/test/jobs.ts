import type { Job, ProgressSnapshot } from '@/features/jobs/api'

export const snapshot = (over: Partial<ProgressSnapshot> = {}): ProgressSnapshot => ({
  jobId: 'j1', status: 'running', seq: 1, ts: new Date().toISOString(), total: 1_203_441, totalKind: 'exact', processed: 402_113,
  percent: 33.41, written: 401_990, skipped: 0, failed: 123, rowsRead: 410_000, bytesWritten: 312 * 1024 * 1024, throughput: 21_450.5,
  peakThroughput: 25_010.2, etaSeconds: 37, elapsedSeconds: 19,
  mappings: [
    { mappingId: 'm1', table: 'public.customer', target: 'shop.sales.customer', phase: 'done', processed: 120_345, total: 120_345, totalKind: 'exact', percent: 100, written: 120_345, skipped: 0, failed: 0 },
    { mappingId: 'm2', table: 'public.orders', target: 'shop.sales.orders', phase: 'migrating', processed: 281_768, total: 1_083_096, totalKind: 'exact', percent: 26.01, written: 281_645, skipped: 0, failed: 123 },
  ],
  errorsByCode: [{ code: 'PARSE_FAILED', count: 120 }, { code: 'DOC_TOO_LARGE', count: 3 }],
  ...over,
})

export const job = (over: Partial<Job> = {}): Job => ({
  id: 'j1', projectId: 'p1', status: 'running', options: { writeMode: 'upsert', batchSize: 1000 }, mappingIds: ['m1', 'm2'],
  createdBy: 'u1', createdAt: new Date(Date.now() - 60_000).toISOString(), updatedAt: new Date().toISOString(), startedAt: new Date(Date.now() - 19_000).toISOString(),
  version: 3, progress: snapshot(over.progress ? over.progress : {}), resumable: false, retryable: false,
  ...over,
})
