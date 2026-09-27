/** Connection details of the e2e stack, as seen from the app containers and from the tests. */
export const env = {
  baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:18081',
  // The app reaches the dev stack by service name.
  source: { host: 'source-pg', port: 5432, database: 'shop', username: 'shop_owner', password: 'shop_owner' },
  target: { connectionString: 'couchbase://couchbase', username: 'Administrator', password: 'password', bucket: 'sandbox' },
  // The tests reach the same services through the published ports.
  couchbaseQuery: process.env.E2E_CB_QUERY ?? 'http://localhost:8093/query/service',
  couchbaseMgmt: process.env.E2E_CB_MGMT ?? 'http://localhost:8091',
  workerContainer: process.env.E2E_WORKER ?? 'omni-data-bus-e2e-worker-1',
  sourceContainer: process.env.E2E_SOURCE_PG ?? 'omni-connect-dev-source-pg-1',
}

export const uid = () => Math.random().toString(36).slice(2, 8)
export const password = 'e2e-Correct-Horse-Battery-9'
