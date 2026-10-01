import { z } from 'zod'

// Couchbase naming rules (OC-014), mirrored from backend/internal/connectors/target/couchbase/names.go.
// Both test suites run the same fixture (testdata/keyspace-names.json) so they can't drift.
const bucketRe = /^[A-Za-z0-9._%-]{1,100}$/
const scopeRe = /^[A-Za-z0-9_%-]{1,251}$/

export const MAX_EXPIRY_SEC = 2147483647

// Directory folders (OC-036/OC-038), mirrored from backend/internal/mapping/location.go: up to three
// "/"-separated segments of [A-Za-z0-9._-], each at most 64 bytes, none "." or "..".
const folderSegRe = /^[A-Za-z0-9._-]{1,64}$/
export const validFolderName = (f: string) => {
  const segs = f.replace(/\/$/, '').split('/')
  return segs.length <= 3 && segs.every((s) => folderSegRe.test(s) && s !== '.' && s !== '..')
}

export const validBucketName = (n: string) => bucketRe.test(n) && !n.startsWith('.')
/** New scopes and collections: names starting with _ or % are reserved. */
export const validScopeName = (n: string) => scopeRe.test(n) && !n.startsWith('_') && !n.startsWith('%')

export const bucketNameSchema = z.string().refine(validBucketName, { message: 'targets.errors.bucketName' })
export const scopeNameSchema = z.string().refine(validScopeName, { message: 'targets.errors.scopeName' })
export const maxExpirySchema = z.number().int().min(0, { message: 'targets.errors.ttl' }).max(MAX_EXPIRY_SEC, { message: 'targets.errors.ttl' })

export const bucketSpecSchema = z
  .object({
    name: bucketNameSchema,
    bucketType: z.enum(['couchbase', 'ephemeral'], { message: 'targets.errors.bucketType' }),
    storageBackend: z.string(),
    ramQuotaMB: z.number().int().min(100, { message: 'targets.errors.ram' }),
    numReplicas: z.number().int().min(0).max(3, { message: 'targets.errors.replicas' }),
  })
  .superRefine((s, ctx) => {
    if (s.bucketType === 'couchbase' && s.storageBackend !== 'couchstore' && s.storageBackend !== 'magma') {
      ctx.addIssue({ code: 'custom', path: ['storageBackend'], message: 'targets.errors.backend' })
    }
    if (s.storageBackend === 'magma' && s.ramQuotaMB < 1024) {
      ctx.addIssue({ code: 'custom', path: ['ramQuotaMB'], message: 'targets.errors.magmaRam' })
    }
  })

export type BucketSpec = z.infer<typeof bucketSpecSchema>
