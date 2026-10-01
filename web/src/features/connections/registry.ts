import type { ComponentType } from 'react'
import { CouchbaseForm } from './CouchbaseForm'
import { DirectoryForm } from './DirectoryForm'
import { PostgresForm } from './PostgresForm'
import type { Connection, ConnectionFormProps } from './types'

/** A summary-card field: a literal value (e.g. a host name) or an i18n key for fixed wording. */
export type SummaryField = { labelKey: string; value?: string; valueKey?: string; mono?: boolean }

/** Per-database UI: the form and the fields shown on the saved-repository summary card. */
export type ConnectionUI = {
  Form: ComponentType<ConnectionFormProps>
  endpoint: (c: Connection) => string
  fields: (c: Connection) => SummaryField[]
}

const str = (v: unknown) => (v === undefined || v === null ? '' : String(v))

export const connectionUIs: Record<string, ConnectionUI> = {
  postgres: {
    Form: PostgresForm,
    endpoint: (c) => `${str(c.config.host)}:${str(c.config.port)}/${str(c.config.database)}`,
    fields: (c) => [
      { labelKey: 'repo.user', value: str(c.config.username), mono: true },
      { labelKey: 'repo.ssl', value: str(c.config.sslMode), mono: true },
    ],
  },
  couchbase: {
    Form: CouchbaseForm,
    endpoint: (c) => str(c.config.connectionString),
    fields: (c) => [
      { labelKey: 'repo.user', value: str(c.config.username), mono: true },
      { labelKey: 'cb.deploymentLabel', valueKey: c.config.deployment === 'capella' ? 'cb.capella' : 'cb.selfManaged' },
      {
        labelKey: 'cb.tlsLabel',
        valueKey: str(c.config.connectionString).startsWith('couchbases://') ? (c.config.tlsVerify === false ? 'cb.tlsOnUnverified' : 'cb.tlsOnVerified') : 'cb.tlsOff',
      },
    ],
  },
  directory: {
    Form: DirectoryForm,
    endpoint: (c) => str(c.config.path),
    fields: (c) => [
      { labelKey: 'dir.layout', valueKey: `dir.layouts.${str(c.config.layout) || 'flat'}` },
      { labelKey: 'dir.durability', valueKey: `dir.durabilities.${str(c.config.durability) || 'file'}` },
    ],
  },
}
