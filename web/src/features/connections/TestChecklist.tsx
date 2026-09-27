import { AlertTriangle, Check, Circle, Copy, X } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { relativeTime } from '@/lib/format'
import type { components } from '@/lib/api/schema'

export type TestResult = components['schemas']['TestResult']
type Step = components['schemas']['TestStep']

function StepIcon({ status }: { status: Step['status'] }) {
  switch (status) {
    case 'ok':
      return <Check className="size-4 text-success-dot" aria-label="ok" />
    case 'warn':
      return <AlertTriangle className="size-4 text-warning-dot" aria-label="warning" />
    case 'fail':
      return <X className="size-4 text-danger" aria-label="failed" />
    default:
      return <Circle className="size-3.5 text-subtle" aria-label="skipped" />
  }
}

function CopyHint({ text }: { text: string }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)
  return (
    <div className="flex items-start gap-2 rounded-md border border-warning-dot/40 bg-surface px-2.5 py-2">
      <code className="flex-1 break-all font-mono text-xs">{text}</code>
      <button
        type="button"
        aria-label={copied ? t('repo.copied') : t('repo.copy')}
        onClick={() => {
          void navigator.clipboard?.writeText(text)
          setCopied(true)
        }}
        className="rounded p-1 text-subtle hover:text-foreground"
      >
        {copied ? <Check className="size-3.5" aria-hidden /> : <Copy className="size-3.5" aria-hidden />}
      </button>
    </div>
  )
}

/** The step-by-step "Test connection" result (OC-009 AC4). */
export function TestChecklist({ result, testedAt, saved }: { result: TestResult; testedAt?: string; saved?: boolean }) {
  const { t } = useTranslation()
  const warnings = result.steps.filter((s) => s.status === 'warn').length
  const badge = !result.ok ? (
    <Badge tone="danger">{t('repo.failed')}</Badge>
  ) : warnings > 0 ? (
    <Badge tone="warning">{t('repo.passedWarn', { count: warnings })}</Badge>
  ) : (
    <Badge tone="success">{t('repo.passed')}</Badge>
  )
  return (
    <section aria-label={t('repo.testTitle')} className="overflow-hidden rounded-[var(--radius-card)] border border-border bg-surface">
      <div className="flex items-center justify-between border-b border-border px-4 py-3.5">
        <h3 className="text-sm font-semibold">{t('repo.testTitle')}</h3>
        {badge}
      </div>
      <ol className="flex flex-col px-4 py-2">
        {result.steps.map((s) => (
          <li key={s.id} className="flex flex-col py-1.5" data-status={s.status}>
            <div className="flex min-h-7 items-center gap-2.5">
              <StepIcon status={s.status} />
              <span className="flex-1 text-[13px]">{s.label}</span>
              {s.status === 'ok' && s.detail && s.id === 'version' ? (
                <span className="font-mono text-xs text-subtle">{s.detail}</span>
              ) : s.status === 'ok' && s.ms ? (
                <span className="font-mono text-xs text-subtle">{t('repo.ms', { ms: s.ms })}</span>
              ) : null}
            </div>
            {(s.status === 'warn' || s.status === 'fail') && (s.detail || s.hint) ? (
              <div className={`ml-6 mt-1 flex flex-col gap-2 rounded-lg p-3 ${s.status === 'fail' ? 'bg-danger-soft' : 'bg-warning-soft'}`}>
                {s.detail ? <p className={`text-[13px] ${s.status === 'fail' ? 'text-danger' : 'text-warning'}`}>{s.detail}</p> : null}
                {s.hint && s.status === 'warn' ? <CopyHint text={s.hint} /> : s.hint ? <p className="text-xs text-danger">{s.hint}</p> : null}
              </div>
            ) : null}
          </li>
        ))}
      </ol>
      <div className="border-t border-border bg-background px-4 py-2.5 text-xs text-subtle">
        {t('repo.testedFooter', { when: relativeTime(testedAt ?? new Date().toISOString()), ms: result.latencyMs })}
        {saved === false ? ` · ${t('repo.notSavedYet')}` : null}
      </div>
    </section>
  )
}
