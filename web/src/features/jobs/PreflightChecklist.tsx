import { AlertTriangle, Check, Circle, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import type { PreflightCheck, PreflightResult } from './api'

function CheckIcon({ status }: { status: PreflightCheck['status'] }) {
  switch (status) {
    case 'ok':
      return <Check className="size-4 shrink-0 text-success-dot" aria-label="ok" />
    case 'warn':
      return <AlertTriangle className="size-4 shrink-0 text-warning-dot" aria-label="warning" />
    case 'fail':
      return <X className="size-4 shrink-0 text-danger" aria-label="failed" />
    default:
      return <Circle className="size-3.5 shrink-0 text-subtle" aria-label="skipped" />
  }
}

/** Pre-flight results in the same style as a connection test (OC-023 AC2). */
export function PreflightChecklist({ result }: { result: PreflightResult }) {
  const { t } = useTranslation()
  const failed = result.checks.filter((c) => c.status === 'fail').length
  const warnings = result.checks.filter((c) => c.status === 'warn').length
  return (
    <section aria-label={t('preflight.title')} className="overflow-hidden rounded-[var(--radius-card)] border border-border bg-surface">
      <div className="flex items-center justify-between border-b border-border px-4 py-3.5">
        <h3 className="text-sm font-semibold">{t('preflight.title')}</h3>
        {failed ? (
          <Badge tone="danger">{t('preflight.failed', { count: failed })}</Badge>
        ) : warnings ? (
          <Badge tone="warning">{t('preflight.passedWarn', { count: warnings })}</Badge>
        ) : (
          <Badge tone="success">{t('preflight.passed')}</Badge>
        )}
      </div>
      <ol className="flex flex-col px-4 py-2">
        {result.checks.map((c) => (
          <li key={c.id} className="flex flex-col py-1.5" data-status={c.status}>
            <div className="flex min-h-7 items-center gap-2.5">
              <CheckIcon status={c.status} />
              <span className="flex-1 text-[13px]">{c.label}</span>
              {c.status === 'ok' && c.detail ? <span className="text-xs text-subtle">{c.detail}</span> : null}
            </div>
            {(c.status === 'warn' || c.status === 'fail') && (c.detail || c.hint) ? (
              <div className={`ml-6 mt-1 flex flex-col gap-1 rounded-lg p-3 ${c.status === 'fail' ? 'bg-danger-soft' : 'bg-warning-soft'}`}>
                {c.detail ? <p className={`text-[13px] ${c.status === 'fail' ? 'text-danger' : 'text-warning'}`}>{c.detail}</p> : null}
                {c.hint ? <p className="text-xs text-muted">{c.hint}</p> : null}
              </div>
            ) : null}
          </li>
        ))}
      </ol>
    </section>
  )
}
