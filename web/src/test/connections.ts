import { http, HttpResponse } from 'msw'
import type { TestResult } from '@/features/connections/TestChecklist'
import type { Connection } from '@/features/connections/types'
import { server } from './msw'

export const okResult: TestResult = {
  ok: true,
  latencyMs: 36,
  steps: [
    { id: 'resolve', label: 'Resolve host', status: 'ok', ms: 3 },
    { id: 'connect', label: 'Open TLS connection', status: 'ok', ms: 18 },
    { id: 'auth', label: 'Authenticate as omni_ro', status: 'ok', ms: 15 },
    { id: 'version', label: 'Server version', status: 'ok', detail: 'PostgreSQL 16.3' },
    {
      id: 'privileges', label: 'Read privileges', status: 'warn',
      detail: "No SELECT on 3 table(s) in schema 'billing'. These tables won't be available to migrate.",
      hint: 'GRANT USAGE ON SCHEMA billing TO omni_ro; GRANT SELECT ON ALL TABLES IN SCHEMA billing TO omni_ro;',
    },
  ],
}

export const failedResult: TestResult = {
  ok: false,
  latencyMs: 20,
  steps: [
    { id: 'resolve', label: 'Resolve host', status: 'ok' },
    { id: 'connect', label: 'Open TLS connection', status: 'ok' },
    { id: 'auth', label: 'Authenticate as omni_ro', status: 'fail', code: 'SOURCE_AUTH_FAILED', detail: 'password authentication failed', hint: 'Check the username and password.' },
    { id: 'version', label: 'Server version', status: 'skipped' },
    { id: 'privileges', label: 'Read privileges', status: 'skipped' },
  ],
}

export const pgConnection = (over: Partial<Connection> = {}): Connection => ({
  id: 'p1::source',
  projectId: 'p1',
  role: 'source',
  type: 'postgres',
  name: 'shop@db.example.com',
  config: { host: 'db.example.com', port: 5432, database: 'shop', username: 'omni_ro', sslMode: 'require', connectTimeoutSec: 10, applicationName: 'omni-data-bus' },
  secrets: { passwordSet: true, caCertSet: false, clientCertSet: false },
  lastTestStatus: 'warn',
  lastTestedAt: new Date().toISOString(),
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
  version: 1,
  ...over,
})

export function mockCatalog() {
  server.use(
    http.get('*/api/v1/connectors', () =>
      HttpResponse.json({
        sources: [{ type: 'postgres', role: 'source', displayName: 'PostgreSQL', icon: 'postgres', capabilities: {} }],
        targets: [{ type: 'couchbase', role: 'target', displayName: 'Couchbase', icon: 'couchbase', capabilities: {} }],
      }),
    ),
  )
}

export function mockProjectConnections(source: Connection | null, target: Connection | null = null) {
  server.use(http.get('*/api/v1/projects/:id/connections', () => HttpResponse.json({ source, target })))
}

export const cbCapabilities = {
  canListBuckets: false, canCreateBucket: false, canManageScopes: true, canWriteDocs: true, rangeScan: true, queryService: true,
  deployment: 'capella', allBuckets: true, reasons: ['Create buckets in the Capella UI, then pick them here.'],
}

export const cbResult: TestResult = {
  ok: true,
  latencyMs: 812,
  steps: [
    { id: 'parse', label: 'Connection string', status: 'ok' },
    { id: 'bootstrap', label: 'Connect and authenticate', status: 'ok', ms: 640 },
    { id: 'services', label: 'Services', status: 'ok', detail: 'Key-Value OK · Query 11 ms' },
    { id: 'version', label: 'Server version', status: 'ok', detail: 'Couchbase Server 8.0.3' },
    { id: 'permissions', label: 'Permissions', status: 'ok' },
  ],
  capabilities: cbCapabilities,
}

export const cbConnection = (over: Partial<Connection> = {}): Connection => ({
  id: 'p1::target',
  projectId: 'p1',
  role: 'target',
  type: 'couchbase',
  name: 'cb.8f2k3x.cloud.couchbase.com',
  config: { deployment: 'capella', connectionString: 'couchbases://cb.8f2k3x.cloud.couchbase.com', username: 'migrator', kvTimeoutMs: 2500, connectTimeoutMs: 10000 },
  secrets: { passwordSet: true, caCertSet: false, clientCertSet: false },
  lastTestStatus: 'ok',
  lastTestedAt: new Date().toISOString(),
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
  version: 1,
  ...over,
})
