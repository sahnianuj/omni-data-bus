import { Check, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'

export type Capabilities = {
  canCreateBucket: boolean
  canManageScopes: boolean
  canWriteDocs: boolean
  queryService: boolean
  rangeScan: boolean
  deployment: string
  reasons?: string[]
}

/** What the target credential may do, from the connection test (OC-012 AC4). */
export function CapabilitiesCard({ caps }: { caps: Capabilities | null }) {
  const { t } = useTranslation()
  const rows: { key: string; ok: boolean; reason?: string }[] = caps
    ? [
        { key: 'cb.caps.write', ok: caps.canWriteDocs },
        { key: 'cb.caps.scopes', ok: caps.canManageScopes },
        { key: 'cb.caps.buckets', ok: caps.canCreateBucket, reason: caps.canCreateBucket ? undefined : caps.reasons?.[0] },
        { key: 'cb.caps.query', ok: caps.queryService },
        { key: 'cb.caps.rangeScan', ok: caps.rangeScan },
      ]
    : []
  return (
    <section aria-label={t('cb.caps.title')} className="rounded-[var(--radius-card)] border border-border bg-surface">
      <h3 className="border-b border-border px-4 py-3.5 text-sm font-semibold">{t('cb.caps.title')}</h3>
      {caps ? (
        <ul className="flex flex-col px-4 py-2">
          {rows.map((r) => (
            <li key={r.key} className="flex items-start gap-2.5 py-2" data-allowed={r.ok}>
              {r.ok ? <Check className="size-4 shrink-0 text-success-dot" aria-label="allowed" /> : <X className="size-4 shrink-0 text-subtle" aria-label="not allowed" />}
              <span className="flex flex-col gap-0.5">
                <span className={r.ok ? 'text-[13px]' : 'text-[13px] text-muted'}>{t(r.key)}</span>
                {r.reason ? <span className="text-xs text-subtle">{r.reason}</span> : null}
              </span>
            </li>
          ))}
        </ul>
      ) : (
        <p className="px-4 py-3 text-[13px] text-muted">{t('cb.caps.unknown')}</p>
      )}
    </section>
  )
}
