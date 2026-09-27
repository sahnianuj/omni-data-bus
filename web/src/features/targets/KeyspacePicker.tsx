import { AlertTriangle, Check, Copy } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Combobox } from '@/components/Combobox'
import { ErrorState } from '@/components/ErrorState'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { humanizeCount } from '@/lib/format'
import { useBuckets, useCollection, useScopes, type TargetCapabilities } from './api'
import { validBucketName } from './names'
import { NewBucketDialog, NewNamePopover } from './NewKeyspaceControls'

export type KeyspaceValue = {
  bucket: string
  scope: string
  collection: string
  /** The collection doesn't exist yet: the migration's pre-flight creates it (OC-014 AC5). */
  createIfMissing?: boolean
}

export const emptyKeyspace: KeyspaceValue = { bucket: '', scope: '', collection: '' }

type Props = {
  connId: string
  value: KeyspaceValue
  onChange: (v: KeyspaceValue) => void
  /** Offer "Create when migration starts" for new collections. */
  allowDeferred?: boolean
  /** Pick bucket and scope only (the Tables step names collections per table). */
  scopeOnly?: boolean
}

/**
 * Bucket → scope → collection on the project's stored target (OC-013), with "+ New …" actions
 * gated by the credential's capabilities (OC-014). Changing the bucket resets scope and
 * collection; changing the scope resets the collection.
 */
export function KeyspacePicker({ connId, value, onChange, allowDeferred, scopeOnly }: Props) {
  const { t } = useTranslation()
  const buckets = useBuckets(connId)
  const scopes = useScopes(connId, value.bucket)
  const [manual, setManual] = useState('')
  const [copied, setCopied] = useState(false)

  const caps = buckets.data?.capabilities as TargetCapabilities | undefined
  const bucketList = buckets.data?.buckets ?? []
  const selectedBucket = bucketList.find((b) => b.name === value.bucket)
  const manualOnly = !!buckets.data?.partial && bucketList.length === 0
  const scope = scopes.data?.find((s) => s.name === value.scope)
  const exists = !!scope?.collections.some((c) => c.name === value.collection)
  const collection = useCollection(connId, value.bucket, value.scope, value.collection, exists && !value.createIfMissing)
  const count = collection.data?.docCountEstimate

  const bucketReason = caps?.canCreateBucket ? undefined : (caps?.reasons?.[0] ?? t('targets.noCreateBucket'))
  const manageReason = !value.bucket ? t('targets.pickBucketFirst') : selectedBucket?.canManage ? undefined : t('targets.noManage')
  const keyspace = value.collection ? `\`${value.bucket}\`.\`${value.scope}\`.\`${value.collection}\`` : ''

  const setBucket = (bucket: string) => onChange({ bucket, scope: '', collection: '' })
  const setScope = (scope: string) => onChange({ ...value, scope, collection: '' })

  if (buckets.status === 'error') return <ErrorState error={buckets.error} onRetry={() => void buckets.refetch()} />

  return (
    <div className="flex flex-col gap-4">
      <div className={scopeOnly ? 'grid grid-cols-1 gap-4 md:grid-cols-2' : 'grid grid-cols-1 gap-4 md:grid-cols-3'}>
        <div className="flex flex-col gap-1">
          {manualOnly ? (
            <form
              className="flex flex-col gap-1.5"
              onSubmit={(e) => {
                e.preventDefault()
                if (validBucketName(manual)) setBucket(manual)
              }}
            >
              <label htmlFor="manual-bucket" className="text-xs font-medium text-muted">
                {t('targets.bucket')}
              </label>
              <div className="flex gap-2">
                <Input id="manual-bucket" value={manual} onChange={(e) => setManual(e.target.value)} placeholder={t('targets.typeBucket')} invalid={manual !== '' && !validBucketName(manual)} />
                <Button type="submit" variant="secondary" disabled={!validBucketName(manual)}>
                  {t('targets.use')}
                </Button>
              </div>
              <p className="text-xs text-subtle">{t('targets.manualHint')}</p>
            </form>
          ) : (
            <Combobox
              label={t('targets.bucket')}
              value={value.bucket}
              loading={buckets.isPending}
              placeholder={t('targets.pickBucket')}
              options={bucketList.map((b) => ({ value: b.name, hint: b.ramQuotaMB ? `${b.ramQuotaMB} MB` : undefined }))}
              onChange={setBucket}
            />
          )}
          <NewBucketDialog connId={connId} disabledReason={bucketReason} onDone={setBucket} />
        </div>
        <div className="flex flex-col gap-1">
          <Combobox
            label={t('targets.scope')}
            value={value.scope}
            disabled={!value.bucket}
            loading={scopes.isPending && !!value.bucket}
            placeholder={value.bucket && scopes.data?.length === 0 ? t('targets.noScopes') : t('targets.pickScope')}
            options={(scopes.data ?? []).map((s) => ({ value: s.name, hint: s.name === '_default' ? t('targets.default') : t('targets.collections', { count: s.collections.length }) }))}
            onChange={setScope}
          />
          <NewNamePopover connId={connId} kind="scope" bucket={value.bucket} disabledReason={manageReason} onDone={(n) => setScope(n)} />
        </div>
        <div className={scopeOnly ? 'hidden' : 'flex flex-col gap-1'}>
          <Combobox
            label={t('targets.collection')}
            value={value.collection}
            disabled={!value.scope}
            placeholder={value.scope && scope?.collections.length === 0 ? t('targets.noCollections') : t('targets.pickCollection')}
            options={(scope?.collections ?? []).map((c) => ({ value: c.name, hint: c.name === '_default' ? t('targets.default') : undefined }))}
            onChange={(collection) => onChange({ ...value, collection, createIfMissing: false })}
            adornment={value.createIfMissing ? <Badge tone="primary">{t('targets.willBeCreated')}</Badge> : null}
          />
          {value.scope ? (
            <NewNamePopover
              connId={connId}
              kind="collection"
              bucket={value.bucket}
              scope={value.scope}
              disabledReason={manageReason}
              allowDeferred={allowDeferred}
              onDone={(collection, deferred) => onChange({ ...value, collection, createIfMissing: deferred })}
            />
          ) : null}
        </div>
      </div>
      {scopes.status === 'error' ? <ErrorState error={scopes.error} /> : null}
      {keyspace ? (
        <div className="flex flex-wrap items-center gap-2 text-[13px]">
          <span className="text-muted">{t('targets.keyspace')}</span>
          <code className="rounded bg-surface-muted px-1.5 py-0.5 font-mono text-xs">{keyspace}</code>
          <Button
            variant="ghost"
            size="icon"
            className="size-7"
            aria-label={t('targets.copyKeyspace')}
            onClick={() => {
              void navigator.clipboard?.writeText(keyspace)
              setCopied(true)
            }}
          >
            {copied ? <Check aria-hidden /> : <Copy aria-hidden />}
          </Button>
          {count != null ? <Badge tone="neutral">{t('targets.docs', { count, formatted: humanizeCount(count) })}</Badge> : null}
        </div>
      ) : null}
      {count != null && count > 0 ? (
        <div role="note" className="flex items-start gap-2 rounded-lg bg-warning-soft px-3.5 py-2.5 text-[13px] text-warning">
          <AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden />
          {t('targets.notEmpty')}
        </div>
      ) : null}
    </div>
  )
}
