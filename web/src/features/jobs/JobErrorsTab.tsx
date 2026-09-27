import { Download, RotateCcw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router'
import { AsyncBoundary } from '@/components/AsyncBoundary'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { EmptyState } from '@/components/EmptyState'
import { ErrorState } from '@/components/ErrorState'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Can } from '@/features/auth/Can'
import { humanizeCount } from '@/lib/format'
import { cn } from '@/lib/utils'
import { errorsExportUrl, useJobErrors, useJobErrorSummary, useRetryJob, type Job, type JobError } from './api'
import { describeError } from './errorCatalog'

const keyText = (k: unknown) => (k == null ? '—' : Array.isArray(k) ? k.map(String).join(', ') : String(k))

/** The job monitor's Errors tab (OC-026 AC7): counts by code, a filterable list, the row drawer. */
export function JobErrorsTab({ job }: { job: Job }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [code, setCode] = useState<string | null>(null)
  const [open, setOpen] = useState<JobError | null>(null)
  const [confirmRetry, setConfirmRetry] = useState(false)
  const summary = useJobErrorSummary(job.id)
  const list = useJobErrors(job.id, code)
  const retry = useRetryJob(job.id, job.projectId)
  return (
    <div className="flex flex-col gap-4">
      <AsyncBoundary query={summary} isEmpty={(s) => s.items.length === 0} empty={<EmptyState title={t('errors.noneTitle')} body={t('errors.noneBody')} />}>
        {(s) => {
          const maxCount = Math.max(...s.items.map((i) => i.count))
          return (
            <section aria-label={t('errors.byCode')} className="flex flex-col gap-2">
              <div className="flex items-center gap-2">
                <h3 className="text-sm font-semibold">{t('errors.byCode')}</h3>
                {s.truncated ? <Badge tone="warning">{t('errors.truncated')}</Badge> : null}
                <div className="ml-auto flex gap-2">
                  <Button variant="secondary" size="sm" asChild>
                    <a href={errorsExportUrl(job.id)} download>
                      <Download aria-hidden />
                      {t('errors.downloadCsv')}
                    </a>
                  </Button>
                  {job.retryable ? (
                    <Can role="editor">
                      <Button size="sm" onClick={() => setConfirmRetry(true)}>
                        <RotateCcw aria-hidden />
                        {t('errors.retry')}
                      </Button>
                    </Can>
                  ) : null}
                </div>
              </div>
              <ul className="flex flex-col gap-1">
                {s.items.map((i) => {
                  const d = describeError(i.code)
                  const active = code === i.code
                  return (
                    <li key={i.stage + i.code}>
                      <button
                        type="button"
                        aria-pressed={active}
                        onClick={() => setCode(active ? null : i.code)}
                        className={cn('grid w-full grid-cols-[14rem_1fr_4rem] items-center gap-3 rounded px-2 py-1.5 text-left text-[13px] hover:bg-surface-muted', active && 'bg-primary-soft')}
                      >
                        <span className="flex flex-col">
                          <span className="font-medium">{d.title}</span>
                          <span className="font-mono text-[11px] text-subtle">{i.code} · {t(`errors.stage.${i.stage}`, { defaultValue: i.stage })}</span>
                        </span>
                        <span className="h-2 overflow-hidden rounded-full bg-surface-muted">
                          <span className="block h-full rounded-full bg-[var(--warning-dot)]" style={{ width: `${(i.count / maxCount) * 100}%` }} />
                        </span>
                        <span className="text-right font-mono text-xs tabular-nums">{humanizeCount(i.count)}</span>
                      </button>
                    </li>
                  )
                })}
              </ul>
            </section>
          )
        }}
      </AsyncBoundary>
      {retry.error ? <ErrorState error={retry.error} /> : null}
      <AsyncBoundary query={list} isEmpty={(l) => l.items.length === 0} empty={null}>
        {(l) => (
          <div className="overflow-auto rounded-[var(--radius-card)] border border-border">
            <table className="w-full border-collapse text-[13px]" aria-label={t('errors.list')}>
              <thead className="bg-surface-muted text-left text-xs text-muted">
                <tr>
                  {['stage', 'code', 'key', 'message', ''].map((c) => (
                    <th key={c} scope="col" className="px-3 py-2 font-medium">{c ? t(`errors.col.${c}`) : <span className="sr-only">{t('errors.viewRow')}</span>}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {l.items.map((e) => (
                  <tr key={e.seq} className="border-t border-border">
                    <td className="px-3 py-2 text-muted">{t(`errors.stage.${e.stage}`, { defaultValue: e.stage })}</td>
                    <td className="px-3 py-2 font-mono text-xs">{e.code}</td>
                    <td className="px-3 py-2 font-mono text-xs">{keyText(e.sourceKey)}</td>
                    <td className="max-w-md truncate px-3 py-2">{e.message}</td>
                    <td className="px-3 py-2 text-right">
                      <Button variant="link" size="sm" className="h-6 px-0" onClick={() => setOpen(e)}>
                        {t('errors.viewRow')}
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </AsyncBoundary>

      <Dialog open={open !== null} onOpenChange={(o) => !o && setOpen(null)}>
        <DialogContent className="left-auto right-0 top-0 flex h-full w-full max-w-xl translate-x-0 translate-y-0 flex-col rounded-none">
          {open ? (
            <>
              <DialogHeader>
                <DialogTitle>{describeError(open.code).title}</DialogTitle>
                <DialogDescription>{describeError(open.code).hint}</DialogDescription>
              </DialogHeader>
              <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-[13px]">
                <dt className="text-muted">{t('errors.col.code')}</dt>
                <dd className="font-mono text-xs">{open.code}</dd>
                <dt className="text-muted">{t('errors.col.key')}</dt>
                <dd className="font-mono text-xs">{keyText(open.sourceKey)}</dd>
                {open.fieldPath ? (
                  <>
                    <dt className="text-muted">{t('errors.field')}</dt>
                    <dd className="font-mono text-xs">{open.fieldPath}</dd>
                  </>
                ) : null}
                <dt className="text-muted">{t('errors.col.message')}</dt>
                <dd>{open.message}</dd>
              </dl>
              <h4 className="mt-2 text-xs font-medium uppercase tracking-wide text-subtle">{t('errors.sample')}</h4>
              <pre className="overflow-auto rounded-lg bg-surface-muted p-3 font-mono text-xs">{JSON.stringify(open.sample ?? null, null, 2)}</pre>
            </>
          ) : null}
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={confirmRetry}
        onOpenChange={setConfirmRetry}
        title={t('errors.retryTitle')}
        description={t('errors.retryBody')}
        confirmLabel={t('errors.retry')}
        loading={retry.isPending}
        onConfirm={() =>
          retry.mutate(code ? [code] : undefined, {
            onSuccess: (res) => {
              setConfirmRetry(false)
              void navigate(`/projects/${job.projectId}/jobs/${res.job.id}`)
            },
            onError: () => setConfirmRetry(false),
          })
        }
      />
    </div>
  )
}
