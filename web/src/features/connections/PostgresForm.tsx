import { zodResolver } from '@hookform/resolvers/zod'
import { ChevronDown, Zap } from 'lucide-react'
import { useState } from 'react'
import { Controller, useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'
import { CertDropzone } from '@/components/CertDropzone'
import { FormField } from '@/components/FormField'
import { SecretInput } from '@/components/SecretInput'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import { parsePgUri } from './parsePgUri'
import type { ConnectionFormProps, ConnectionPayload } from './types'

const SSL_MODES = ['disable', 'prefer', 'require', 'verify-ca', 'verify-full'] as const
type SslMode = (typeof SSL_MODES)[number]

type Values = {
  host: string
  port: string
  database: string
  username: string
  password?: string // undefined = keep the stored password
  sslMode: SslMode
  caCertPem: string | null
  clientCertPem: string | null
  clientKeyPem: string | null
  connectTimeoutSec: string
  applicationName: string
}

const HOST = /^(\[[0-9a-fA-F:.]+\]|[0-9a-fA-F:]+|[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?)$/

/** PostgreSQL source form (OC-009). Values are validated on the client before any request. */
export function PostgresForm({ existing, testing, saving, onTest, onSave, onCancel }: ConnectionFormProps) {
  const { t } = useTranslation()
  const cfg = (existing?.config ?? {}) as Partial<Record<keyof Values, unknown>>
  const storedPassword = !!existing?.secrets.passwordSet
  const [pasting, setPasting] = useState(false)
  const [uri, setUri] = useState('')
  const [uriError, setUriError] = useState<string | null>(null)
  const [advanced, setAdvanced] = useState(false)

  const schema = z
    .object({
      host: z.string().trim().regex(HOST, t('pg.errors.host')),
      port: z.string().refine((v) => /^\d+$/.test(v) && +v >= 1 && +v <= 65535, t('pg.errors.port')),
      database: z.string().trim().min(1, t('pg.errors.database')).max(63),
      username: z.string().trim().min(1, t('pg.errors.username')).max(63),
      password: z.string().optional(),
      sslMode: z.enum(SSL_MODES),
      caCertPem: z.string().nullable(),
      clientCertPem: z.string().nullable(),
      clientKeyPem: z.string().nullable(),
      connectTimeoutSec: z.string().refine((v) => /^\d+$/.test(v) && +v >= 1 && +v <= 60, t('pg.errors.timeout')),
      applicationName: z.string().max(63),
    })
    .refine((v) => storedPassword || (v.password ?? '').length > 0, { path: ['password'], message: t('pg.errors.password') })

  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: {
      host: String(cfg.host ?? ''),
      port: String(cfg.port ?? 5432),
      database: String(cfg.database ?? ''),
      username: String(cfg.username ?? ''),
      password: storedPassword ? undefined : '',
      sslMode: (cfg.sslMode as SslMode) ?? 'require',
      caCertPem: null,
      clientCertPem: null,
      clientKeyPem: null,
      connectTimeoutSec: String(cfg.connectTimeoutSec ?? 10),
      applicationName: String(cfg.applicationName ?? 'omni-data-bus'),
    },
  })
  const { errors } = form.formState
  const sslMode = useWatch({ control: form.control, name: 'sslMode' })

  const toPayload = (v: Values): ConnectionPayload => {
    const secrets: ConnectionPayload['secrets'] = {}
    if (v.password) secrets.password = v.password
    if (v.caCertPem) secrets.caCertPem = v.caCertPem
    if (v.clientCertPem) secrets.clientCertPem = v.clientCertPem
    if (v.clientKeyPem) secrets.clientKeyPem = v.clientKeyPem
    return {
      config: {
        host: v.host.trim(), port: Number(v.port), database: v.database.trim(), username: v.username.trim(), sslMode: v.sslMode,
        connectTimeoutSec: Number(v.connectTimeoutSec), applicationName: v.applicationName.trim() || 'omni-data-bus',
      },
      secrets,
    }
  }

  const applyUri = () => {
    const p = parsePgUri(uri)
    if (!p) {
      setUriError(t('pg.uriInvalid'))
      return
    }
    const opts = { shouldValidate: form.formState.isSubmitted, shouldDirty: true }
    form.setValue('host', p.host, opts)
    if (p.port) form.setValue('port', String(p.port), opts)
    if (p.database) form.setValue('database', p.database, opts)
    if (p.username) form.setValue('username', p.username, opts)
    if (p.password) form.setValue('password', p.password, opts)
    if (p.sslMode) form.setValue('sslMode', p.sslMode, opts)
    setUri('') // the raw URI (with its password) is not kept
    setUriError(null)
    setPasting(false)
  }

  return (
    <form noValidate onSubmit={(e) => e.preventDefault()} className="flex flex-col gap-4 rounded-[var(--radius-card)] border border-border bg-surface p-5">
      <div className="flex items-center justify-between">
        <h3 className="text-[13px] font-semibold text-muted">{t('pg.connection')}</h3>
        <button type="button" onClick={() => setPasting((v) => !v)} className="text-[13px] font-medium text-primary-soft-foreground hover:underline">
          {t('pg.pasteUri')}
        </button>
      </div>
      {pasting ? (
        <div className="flex flex-col gap-1.5">
          <div className="flex gap-2">
            <Input aria-label={t('pg.pasteUri')} type="password" autoComplete="off" placeholder={t('pg.pastePlaceholder')} value={uri} onChange={(e) => setUri(e.target.value)} className="font-mono text-xs" />
            <Button variant="secondary" onClick={applyUri}>
              {t('pg.apply')}
            </Button>
          </div>
          {uriError ? (
            <p role="alert" className="text-xs text-danger">
              {uriError}
            </p>
          ) : null}
        </div>
      ) : null}
      <div className="grid grid-cols-4 gap-3">
        <div className="col-span-3">
          <FormField id="pg-host" label={t('pg.host')} error={errors.host?.message}>
            <Input id="pg-host" className="font-mono" invalid={!!errors.host} {...form.register('host')} />
          </FormField>
        </div>
        <FormField id="pg-port" label={t('pg.port')} error={errors.port?.message}>
          <Input id="pg-port" inputMode="numeric" className="font-mono" invalid={!!errors.port} {...form.register('port')} />
        </FormField>
        <div className="col-span-2">
          <FormField id="pg-database" label={t('pg.database')} error={errors.database?.message}>
            <Input id="pg-database" className="font-mono" invalid={!!errors.database} {...form.register('database')} />
          </FormField>
        </div>
        <div className="col-span-2">
          <FormField id="pg-username" label={t('pg.username')} error={errors.username?.message}>
            <Input id="pg-username" autoComplete="off" className="font-mono" invalid={!!errors.username} {...form.register('username')} />
          </FormField>
        </div>
        <div className="col-span-4">
          <FormField id="pg-password" label={t('pg.password')} error={errors.password?.message}>
            <Controller
              control={form.control}
              name="password"
              render={({ field }) => <SecretInput id="pg-password" stored={storedPassword} value={field.value} onChange={field.onChange} invalid={!!errors.password} />}
            />
          </FormField>
        </div>
      </div>
      <div className="h-px bg-border" />
      <div className="flex flex-col gap-2">
        <span id="pg-ssl-label" className="text-[13px] font-medium">
          {t('pg.security')}
        </span>
        <Controller
          control={form.control}
          name="sslMode"
          render={({ field }) => (
            <div role="radiogroup" aria-labelledby="pg-ssl-label" className="flex gap-0.5 rounded-lg bg-surface-muted p-[3px]">
              {SSL_MODES.map((m) => (
                <button
                  key={m}
                  type="button"
                  role="radio"
                  aria-checked={field.value === m}
                  onClick={() => field.onChange(m)}
                  className={cn('h-7 flex-1 rounded-md font-mono text-xs text-muted', field.value === m && 'bg-surface font-medium text-foreground shadow-sm')}
                >
                  {m}
                </button>
              ))}
            </div>
          )}
        />
        <p className="text-xs text-subtle">{t(`pg.sslHelp.${sslMode}`)}</p>
        {sslMode === 'verify-ca' || sslMode === 'verify-full' ? (
          <div className="flex flex-col gap-3 pt-1">
            <CertDropzone label={t('pg.caCert')} storedFingerprint={existing?.secrets.caCertFingerprint} onChange={(pem) => form.setValue('caCertPem', pem)} />
            <div className="grid grid-cols-2 gap-3">
              <CertDropzone label={t('pg.clientCert')} onChange={(pem) => form.setValue('clientCertPem', pem)} />
              <CertDropzone label={t('pg.clientKey')} onChange={(pem) => form.setValue('clientKeyPem', pem)} />
            </div>
          </div>
        ) : null}
      </div>
      <button type="button" aria-expanded={advanced} onClick={() => setAdvanced((v) => !v)} className="flex h-8 items-center justify-between text-[13px] text-muted">
        <span>{t('pg.advanced')}</span>
        <ChevronDown className={cn('size-3.5 transition-transform', advanced && 'rotate-180')} aria-hidden />
      </button>
      {advanced ? (
        <div className="grid grid-cols-2 gap-3">
          <FormField id="pg-timeout" label={t('pg.timeout')} error={errors.connectTimeoutSec?.message}>
            <Input id="pg-timeout" inputMode="numeric" invalid={!!errors.connectTimeoutSec} {...form.register('connectTimeoutSec')} />
          </FormField>
          <FormField id="pg-app" label={t('pg.appName')}>
            <Input id="pg-app" {...form.register('applicationName')} />
          </FormField>
        </div>
      ) : null}
      <div className="flex items-center justify-end gap-2 border-t border-border pt-4">
        {onCancel ? (
          <Button variant="ghost" onClick={onCancel}>
            {t('repo.cancelEdit')}
          </Button>
        ) : null}
        <Button variant="secondary" loading={testing} onClick={form.handleSubmit((v) => onTest(toPayload(v)))}>
          <Zap aria-hidden />
          {t('repo.test')}
        </Button>
        <Button loading={saving} onClick={form.handleSubmit((v) => onSave(toPayload(v)))}>
          {t('repo.save')}
        </Button>
      </div>
    </form>
  )
}
