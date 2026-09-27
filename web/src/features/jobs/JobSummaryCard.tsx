import { AlertTriangle, CheckCircle2, ChevronDown, Download, XCircle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { formatDuration, formatRate } from '@/lib/format'
import { cn } from '@/lib/utils'
import { errorsExportUrl, reportUrl, useJobReport, type Job } from './api'
import { VerificationSection } from './VerificationSection'

const fmt = new Intl.NumberFormat('en')

/** Split button: HTML report by default, with JSON and CSV in the menu (OC-030 AC5). */
function ReportDownload({ jobId }: { jobId: string }) {
  const { t } = useTranslation()
  return (
    <div className="flex">
      <Button variant="secondary" size="sm" className="rounded-r-none" asChild>
        <a href={reportUrl(jobId, 'html')} download>
          <Download aria-hidden />
          {t('verify.downloadHtml')}
        </a>
      </Button>
      <Popover>
        <PopoverTrigger asChild>
          <Button variant="secondary" size="sm" className="rounded-l-none border-l border-border px-2" aria-label={t('verify.downloadMore')}>
            <ChevronDown aria-hidden />
          </Button>
        </PopoverTrigger>
        <PopoverContent align="end" className="flex w-56 flex-col p-1">
          {(
            [
              ['html', 'verify.asHtml'],
              ['json', 'verify.asJson'],
              ['csv', 'verify.asCsv'],
            ] as const
          ).map(([f, label]) => (
            <a key={f} href={reportUrl(jobId, f)} download className="rounded-md px-3 py-1.5 text-[13px] hover:bg-surface-muted">
              {t(label)}
            </a>
          ))}
        </PopoverContent>
      </Popover>
    </div>
  )
}

/** Shown when the job ends (OC-028 AC6): totals, duration, average rate, and follow-ups. */
export function JobSummaryCard({ job, onViewErrors }: { job: Job; onViewErrors: () => void }) {
  const { t } = useTranslation()
  const p = job.progress
  const seconds = Math.max(1, p.elapsedSeconds)
  const ok = job.status === 'completed'
  const warn = job.status === 'completed_with_errors'
  const verifies = job.options.verifySampleSize !== 0
  const report = useJobReport(job.id, true, verifies)
  return (
    <section
      aria-label={t('monitor.summary')}
      className={cn('flex flex-col gap-3 rounded-[var(--radius-card)] border p-4', ok ? 'border-success-dot bg-success-soft' : warn ? 'border-[var(--warning-dot)] bg-warning-soft' : 'border-border bg-surface')}
    >
      <h3 className="flex items-center gap-2 text-sm font-semibold">
        {ok ? <CheckCircle2 className="size-4 text-success-dot" aria-hidden /> : warn ? <AlertTriangle className="size-4 text-warning" aria-hidden /> : <XCircle className="size-4 text-danger" aria-hidden />}
        {job.status === 'cancelled'
          ? t('monitor.summaryCancelled', { n: fmt.format(p.written) })
          : job.status === 'failed'
            ? t('monitor.summaryFailed', { n: fmt.format(p.written), reason: job.reasonDetail || job.reason || '' })
            : t('monitor.summaryDone', { n: fmt.format(p.written), duration: formatDuration(seconds), rate: formatRate(p.written / seconds) })}
      </h3>
      <p className="text-[13px] text-muted">
        {t('monitor.summaryCounts', { skipped: fmt.format(p.skipped), failed: fmt.format(p.failed) })}
      </p>
      {report.data ? <VerificationSection report={report.data} verifies={verifies} /> : null}
      <div className="flex flex-wrap gap-2">
        <ReportDownload jobId={job.id} />
        {p.failed > 0 ? (
          <>
            <Button variant="secondary" size="sm" asChild>
              <a href={errorsExportUrl(job.id)} download>
                <Download aria-hidden />
                {t('errors.downloadCsv')}
              </a>
            </Button>
            <Button variant="secondary" size="sm" onClick={onViewErrors}>
              {t('monitor.viewErrors')}
            </Button>
          </>
        ) : null}
      </div>
    </section>
  )
}
