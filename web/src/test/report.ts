import type { JobReport, VerificationResult } from '@/features/jobs/api'

export const verification = (over: Partial<VerificationResult> = {}): VerificationResult => ({
  mappingId: 'm1', result: 'passed', avgDocBytes: 512, throughputAvg: 20_000, throughputPeak: 25_000, durationSeconds: 6,
  counts: { sourceRows: 100, written: 100, skipped: 0, failed: 0, duplicateKeys: 0, expectedDocs: 100, reconciled: true, warnings: [], messages: [] },
  sample: { checked: 100, matched: 100, mismatched: 0, missing: 0, sourceChanged: 0, missingKeys: [], mismatches: [] },
  ...over,
})

export const report = (mappings: JobReport['mappings']): JobReport => ({
  generatedAt: new Date().toISOString(),
  job: {
    id: 'j1', status: 'completed', projectId: 'p1', projectName: 'Shop', source: { type: 'postgresql', host: 'pg:5432', database: 'shop' },
    target: { type: 'couchbase', host: 'cb.local' }, keyspaces: ['shop.sales.customer'], startedBy: 'u1', durationSeconds: 12, options: {},
  },
  mappings,
  errors: { total: 0, byCode: [], exportUrl: '/api/v1/jobs/j1/errors/export?format=csv' },
  specs: {},
})

export const mappingReport = (id: string, table: string, v?: VerificationResult): JobReport['mappings'][number] => ({
  mappingId: id, table, target: `shop.sales.${table}`, keyTemplate: `${table}::{id}`, rowsRead: 100, written: 100, skipped: 0, failed: 0, verification: v,
})
