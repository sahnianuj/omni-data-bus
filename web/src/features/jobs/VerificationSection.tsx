import { AlertTriangle, CheckCircle2, Loader2, XCircle } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { JsonDiff } from '@/components/JsonDiff'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import type { JobReport } from './api'

type MappingReport = JobReport['mappings'][number]

const icon = {
  passed: <CheckCircle2 className="size-4 shrink-0 text-success-dot" aria-hidden />,
  passed_with_warnings: <AlertTriangle className="size-4 shrink-0 text-warning" aria-hidden />,
  failed: <XCircle className="size-4 shrink-0 text-danger" aria-hidden />,
  skipped: <AlertTriangle className="size-4 shrink-0 text-subtle" aria-hidden />,
}

const parse = (v: unknown) => {
  if (typeof v !== 'string') return v
  try {
    return JSON.parse(v)
  } catch {
    return v
  }
}

/** Each table's verification in the job summary (OC-030 AC5), with a drawer of mismatches. */
export function VerificationSection({ report, verifies }: { report: JobReport; verifies: boolean }) {
  const { t, i18n } = useTranslation()
  const [open, setOpen] = useState<MappingReport | null>(null)
  const n = (x: number) => x.toLocaleString(i18n.language)
  return (
    <section aria-label={t('verify.title')} className="flex flex-col gap-2">
      <h4 className="text-xs font-semibold uppercase tracking-wide text-muted">{t('verify.title')}</h4>
      {!verifies ? <p className="text-[13px] text-muted">{t('verify.none')}</p> : null}
      {verifies ? (
        <ul className="flex flex-col gap-1.5 text-[13px]">
          {report.mappings.map((m) => {
            const v = m.verification
            if (!v)
              return (
                <li key={m.mappingId} className="flex items-center gap-2 text-muted">
                  <Loader2 className="size-4 animate-spin" aria-hidden />
                  <span className="font-mono text-xs">{m.table}</span> {t('verify.pending')}
                </li>
              )
            const s = v.sample
            const parts = [
              s ? t('verify.samples', { matched: n(s.matched), checked: n(s.checked) }) : null,
              s?.mismatched ? t('verify.mismatched', { count: s.mismatched }) : null,
              s?.missing ? t('verify.missing', { count: s.missing }) : null,
              s?.sourceChanged ? t('verify.sourceChanged', { count: s.sourceChanged }) : null,
              v.counts.reconciled ? t('verify.reconciled') : t('verify.notReconciled'),
              v.counts.targetCount != null ? t('verify.targetCount', { count: v.counts.targetCount }) : null,
            ].filter(Boolean)
            return (
              <li key={m.mappingId} className="flex flex-col gap-0.5" data-testid={`verify-${m.mappingId}`}>
                <div className="flex flex-wrap items-center gap-2">
                  {icon[v.result]}
                  <span className="font-mono text-xs">{m.table}</span>
                  <span className="font-medium">{t(`verify.${v.result}`)}</span>
                  <span className="text-muted">· {parts.join(' · ')}</span>
                  {s && s.mismatches.length > 0 ? (
                    <Button variant="link" size="sm" className="h-auto px-0" onClick={() => setOpen(m)}>
                      {t('verify.viewMismatches', { count: s.mismatched })}
                    </Button>
                  ) : null}
                </div>
                {[...v.counts.messages, ...(v.error ? [v.error] : [])].map((msg) => (
                  <p key={msg} className="pl-6 text-xs text-warning">
                    {msg}
                  </p>
                ))}
              </li>
            )
          })}
        </ul>
      ) : null}
      <Dialog open={open !== null} onOpenChange={(o) => !o && setOpen(null)}>
        <DialogContent className="max-h-[85vh] max-w-4xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{t('verify.mismatchesTitle', { table: open?.table ?? '' })}</DialogTitle>
            <DialogDescription>{t('verify.mismatchesBody')}</DialogDescription>
          </DialogHeader>
          {(open?.verification?.sample?.mismatches ?? []).map((mm) => (
            <article key={mm.key} className="flex flex-col gap-2 border-t border-border pt-3">
              <h5 className="font-mono text-xs font-semibold">{mm.key}</h5>
              <p className="text-xs text-muted">{t('verify.paths', { paths: mm.diffs.map((d) => d.path).join(', ') })}</p>
              <JsonDiff expected={parse(mm.expected)} actual={parse(mm.actual)} paths={mm.diffs.map((d) => d.path)} />
            </article>
          ))}
        </DialogContent>
      </Dialog>
    </section>
  )
}
