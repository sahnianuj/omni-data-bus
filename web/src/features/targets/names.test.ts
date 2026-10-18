import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { bucketSpecSchema, maxExpirySchema, validBucketName, validFolderName, validScopeName } from './names'

type Case = { name: string; valid: boolean }
type Fixture = {
  bucket: Case[]
  scope: Case[]
  collection: Case[]
  bucketSpec: { spec: Record<string, unknown>; valid: boolean; field?: string }[]
  maxExpirySec: { value: number; valid: boolean }[]
}

// The same file drives backend/internal/connectors/target/couchbase/names_test.go.
const fixture = JSON.parse(
  readFileSync(resolve(process.cwd(), '../backend/internal/connectors/target/couchbase/testdata/keyspace-names.json'), 'utf8'),
) as Fixture

describe('keyspace names', () => {
  it('AC1: shared fixture parity', () => {
    for (const c of fixture.bucket) expect(validBucketName(c.name), `bucket ${c.name}`).toBe(c.valid)
    for (const c of fixture.scope) expect(validScopeName(c.name), `scope ${c.name}`).toBe(c.valid)
    for (const c of fixture.collection) expect(validScopeName(c.name), `collection ${c.name}`).toBe(c.valid)
    for (const c of fixture.bucketSpec) {
      const r = bucketSpecSchema.safeParse(c.spec)
      expect(r.success, JSON.stringify(c.spec)).toBe(c.valid)
      if (!r.success && c.field) expect(r.error.issues[0]?.path[0]).toBe(c.field)
    }
    for (const c of fixture.maxExpirySec) expect(maxExpirySchema.safeParse(c.value).success, `ttl ${c.value}`).toBe(c.valid)
  })
})

describe('validFolderName (OC-186)', () => {
  it('refuses folders Windows reserves or that end in a dot, on every OS', () => {
    for (const bad of ['con', 'CON', 'orders/Nul', 'aux.json', 'com1', 'lpt9/x', 'orders.', 'a/b.']) expect(validFolderName(bad)).toBe(false)
    for (const ok of ['orders', 'console', 'com10', 'contacts.v2', 'a/b.c', 'lpt']) expect(validFolderName(ok)).toBe(true)
  })
})
