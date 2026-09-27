import type { components } from '@/lib/api/schema'

export type SecretsPatch = components['schemas']['SecretsPatch']
export type SecretMeta = components['schemas']['SecretMeta']
export type Connection = components['schemas']['Connection']

/** What a connection form hands to Test / Save. */
export type ConnectionPayload = { config: Record<string, unknown>; secrets: SecretsPatch }

/** Props every database-specific form accepts (PostgreSQL, Couchbase, …). */
export type ConnectionFormProps = {
  /** Present when editing a saved connection. */
  existing?: Connection
  testing: boolean
  saving: boolean
  onTest: (p: ConnectionPayload) => void
  onSave: (p: ConnectionPayload) => void
  onCancel?: () => void
}
