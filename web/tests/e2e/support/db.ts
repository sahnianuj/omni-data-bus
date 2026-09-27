import { execFileSync } from 'node:child_process'
import { env } from './env'

/** Runs a SQL++ statement against the dev Couchbase (as Administrator). */
export async function n1ql<T = unknown>(statement: string): Promise<T[]> {
  const res = await fetch(env.couchbaseQuery, {
    method: 'POST',
    headers: { Authorization: 'Basic ' + Buffer.from('Administrator:password').toString('base64'), 'Content-Type': 'application/json' },
    body: JSON.stringify({ statement, scan_consistency: 'request_plus' }),
  })
  const body = (await res.json()) as { results: T[]; errors?: unknown }
  if (!res.ok) throw new Error(`${statement}: ${JSON.stringify(body.errors)}`)
  return body.results
}

/** Counts the documents in sandbox.<scope>.<collection>. */
export async function docCount(scope: string, collection: string): Promise<number> {
  const [n] = await n1ql<number>(`SELECT RAW COUNT(*) FROM \`${env.target.bucket}\`.\`${scope}\`.\`${collection}\``)
  return n ?? 0
}

/** Fetches one document by key. */
export async function getDoc<T = Record<string, unknown>>(scope: string, collection: string, key: string): Promise<T | undefined> {
  const [d] = await n1ql<T>(`SELECT RAW c FROM \`${env.target.bucket}\`.\`${scope}\`.\`${collection}\` AS c USE KEYS ${JSON.stringify(key)}`)
  return d
}

/** Runs SQL in the sample PostgreSQL container (psql, tab-separated, no headers). */
export function psql(sql: string): string {
  return execFileSync('docker', ['exec', env.sourceContainer, 'psql', '-U', 'shop_owner', '-d', 'shop', '-At', '-c', sql], { encoding: 'utf8' }).trim()
}

/** Row count of a source table. */
export const rowCount = (table: string) => Number(psql(`SELECT count(*) FROM ${table}`))

/** Kills / starts a container of the e2e stack. */
export const docker = (...args: string[]) => execFileSync('docker', args, { encoding: 'utf8' }).trim()

/** Drops a scope from the sandbox bucket (cleanup). */
export async function dropScope(scope: string) {
  await fetch(`${env.couchbaseMgmt}/pools/default/buckets/${env.target.bucket}/scopes/${scope}`, {
    method: 'DELETE',
    headers: { Authorization: 'Basic ' + Buffer.from('Administrator:password').toString('base64') },
  })
}
