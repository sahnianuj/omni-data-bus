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
import type { ConnectionFormProps, ConnectionPayload } from './types'

type Deployment = 'self-managed' | 'capella'
type Values = {
  deployment: Deployment
  connectionString: string
  username: string
  password?: string
  tlsVerify: boolean
  caCertPem: string | null
  kvTimeoutMs: string
  connectTimeoutMs: string
}

const inRange = (min: number, max: number) => (v: string) => /^\d+$/.test(v) && +v >= min && +v <= max

/** Couchbase target form (OC-012): self-managed or Capella. */
export function CouchbaseForm({ existing, testing, saving, onTest, onSave, onCancel }: ConnectionFormProps) {
  const { t } = useTranslation()
  const cfg = (existing?.config ?? {}) as Record<string, unknown>
  const storedPassword = !!existing?.secrets.passwordSet
  const [advanced, setAdvanced] = useState(false)

  const schema = z
    .object({
      deployment: z.enum(['self-managed', 'capella']),
      connectionString: z.string().trim().regex(/^couchbases?:\/\/\S+$/, t('cb.errors.connectionString')),
      username: z.string().trim().min(1, t('cb.errors.username')),
      password: z.string().optional(),
      tlsVerify: z.boolean(),
      caCertPem: z.string().nullable(),
      kvTimeoutMs: z.string().refine(inRange(100, 60000), t('cb.errors.kvTimeout')),
      connectTimeoutMs: z.string().refine(inRange(1000, 120000), t('cb.errors.connectTimeout')),
    })
    .refine((v) => v.deployment !== 'capella' || v.connectionString.trim().startsWith('couchbases://'), {
      path: ['connectionString'],
      message: t('cb.errors.capellaScheme'),
    })
    .refine((v) => storedPassword || (v.password ?? '').length > 0, { path: ['password'], message: t('cb.errors.password') })

  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: {
      deployment: (cfg.deployment as Deployment) ?? 'self-managed',
      connectionString: String(cfg.connectionString ?? ''),
      username: String(cfg.username ?? ''),
      password: storedPassword ? undefined : '',
      tlsVerify: cfg.tlsVerify !== false,
      caCertPem: null,
      kvTimeoutMs: String(cfg.kvTimeoutMs ?? 2500),
      connectTimeoutMs: String(cfg.connectTimeoutMs ?? 10000),
    },
  })
  const { errors } = form.formState
  const deployment = useWatch({ control: form.control, name: 'deployment' })

  const toPayload = (v: Values): ConnectionPayload => {
    const secrets: ConnectionPayload['secrets'] = {}
    if (v.password) secrets.password = v.password
    if (v.caCertPem) secrets.caCertPem = v.caCertPem
    const config: Record<string, unknown> = {
      deployment: v.deployment, connectionString: v.connectionString.trim(), username: v.username.trim(),
      kvTimeoutMs: Number(v.kvTimeoutMs), connectTimeoutMs: Number(v.connectTimeoutMs),
    }
    if (v.deployment === 'self-managed') config.tlsVerify = v.tlsVerify
    return { config, secrets }
  }

  return (
    <form noValidate onSubmit={(e) => e.preventDefault()} className="flex flex-col gap-4 rounded-[var(--radius-card)] border border-border bg-surface p-5">
      <div className="flex flex-col gap-2">
        <span id="cb-deployment-label" className="text-[13px] font-medium">
          {t('cb.deployment')}
        </span>
        <Controller
          control={form.control}
          name="deployment"
          render={({ field }) => (
            <div role="radiogroup" aria-labelledby="cb-deployment-label" className="flex gap-0.5 rounded-lg bg-surface-muted p-[3px]">
              {(['self-managed', 'capella'] as const).map((d) => (
                <button
                  key={d}
                  type="button"
                  role="radio"
                  aria-checked={field.value === d}
                  onClick={() => field.onChange(d)}
                  className={cn('h-8 flex-1 rounded-md text-[13px] text-muted', field.value === d && 'bg-surface font-medium text-foreground shadow-sm')}
                >
                  {t(d === 'capella' ? 'cb.capella' : 'cb.selfManaged')}
                </button>
              ))}
            </div>
          )}
        />
        {deployment === 'capella' ? <p className="text-xs text-subtle">{t('cb.capellaHelp')}</p> : null}
      </div>
      <FormField id="cb-connstr" label={t('cb.connectionString')} error={errors.connectionString?.message}>
        <Input
          id="cb-connstr"
          className="font-mono"
          placeholder={deployment === 'capella' ? 'couchbases://cb.xxxxxxxx.cloud.couchbase.com' : 'couchbase://localhost'}
          invalid={!!errors.connectionString}
          {...form.register('connectionString')}
        />
      </FormField>
      <div className="grid grid-cols-2 gap-3">
        <FormField id="cb-username" label={t('cb.username')} error={errors.username?.message}>
          <Input id="cb-username" autoComplete="off" className="font-mono" invalid={!!errors.username} {...form.register('username')} />
        </FormField>
        <FormField id="cb-password" label={t('cb.password')} error={errors.password?.message}>
          <Controller
            control={form.control}
            name="password"
            render={({ field }) => <SecretInput id="cb-password" stored={storedPassword} value={field.value} onChange={field.onChange} invalid={!!errors.password} />}
          />
        </FormField>
      </div>
      <div className="h-px bg-border" />
      <div className="flex flex-col gap-3">
        <span className="text-[13px] font-medium">{t('cb.security')}</span>
        {deployment === 'self-managed' ? (
          <Controller
            control={form.control}
            name="tlsVerify"
            render={({ field }) => (
              <label className="flex items-start gap-2.5 text-[13px]">
                <input type="checkbox" checked={field.value} onChange={(e) => field.onChange(e.target.checked)} className="mt-0.5 size-4 accent-[var(--primary)]" />
                <span className="flex flex-col gap-0.5">
                  <span>{t('cb.tlsVerify')}</span>
                  <span className="text-xs text-subtle">{t('cb.tlsVerifyHelp')}</span>
                </span>
              </label>
            )}
          />
        ) : null}
        <CertDropzone label={t('cb.caCert')} storedFingerprint={existing?.secrets.caCertFingerprint} onChange={(pem) => form.setValue('caCertPem', pem)} />
      </div>
      <button type="button" aria-expanded={advanced} onClick={() => setAdvanced((v) => !v)} className="flex h-8 items-center justify-between text-[13px] text-muted">
        <span>{t('cb.advanced')}</span>
        <ChevronDown className={cn('size-3.5 transition-transform', advanced && 'rotate-180')} aria-hidden />
      </button>
      {advanced ? (
        <div className="grid grid-cols-2 gap-3">
          <FormField id="cb-kv" label={t('cb.kvTimeout')} error={errors.kvTimeoutMs?.message}>
            <Input id="cb-kv" inputMode="numeric" invalid={!!errors.kvTimeoutMs} {...form.register('kvTimeoutMs')} />
          </FormField>
          <FormField id="cb-connect" label={t('cb.connectTimeout')} error={errors.connectTimeoutMs?.message}>
            <Input id="cb-connect" inputMode="numeric" invalid={!!errors.connectTimeoutMs} {...form.register('connectTimeoutMs')} />
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
