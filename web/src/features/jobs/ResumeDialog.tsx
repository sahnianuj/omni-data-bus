import { AlertTriangle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useResumeJob, type Job, type ResumeMode } from './api'

type Props = {
  job: Job
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Called with the job that now runs (a new one after "restart"). */
  onResumed?: (job: Job, mode: ResumeMode) => void
}

/**
 * Confirms a resume that isn't a plain continue (OC-029 AC7): a mapping edited since the job
 * started (the job keeps its frozen copy unless restarted), or a keyless table re-read from the start.
 */
export function ResumeDialog({ job, open, onOpenChange, onResumed }: Props) {
  const { t, i18n } = useTranslation()
  const resume = useResumeJob(job.id, job.projectId)
  const info = job.resumeInfo
  const changed = info?.changed ?? []
  const restart = info?.restart ?? []
  const list = (refs: { table: string }[]) => new Intl.ListFormat(i18n.language, { type: 'conjunction' }).format(refs.map((r) => r.table))
  const go = (mode: ResumeMode) =>
    resume.mutate(mode, {
      onSuccess: (j) => {
        onOpenChange(false)
        onResumed?.(j, mode)
      },
    })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('monitor.resumeTitle')}</DialogTitle>
          <DialogDescription>{t('monitor.resumeRows', { n: (info?.rowsDone ?? 0).toLocaleString(i18n.language) })}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3 text-sm">
          {changed.length > 0 ? (
            <p role="alert" className="flex items-start gap-2 rounded-lg bg-warning-soft px-3.5 py-2.5 text-[13px] text-warning">
              <AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden />
              <span>
                {t('monitor.resumeChanged')}
                <span className="mt-1 block">{t('monitor.resumeChangedList', { tables: list(changed) })}</span>
              </span>
            </p>
          ) : null}
          {restart.length > 0 ? (
            <p role="alert" className="flex items-start gap-2 rounded-lg bg-warning-soft px-3.5 py-2.5 text-[13px] text-warning">
              <AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden />
              <span>{t('monitor.resumeNoPk', { tables: list(restart) })}</span>
            </p>
          ) : null}
        </div>
        {resume.error ? <ErrorState error={resume.error} /> : null}
        <DialogFooter>
          <Button variant="secondary" onClick={() => onOpenChange(false)}>
            {t('common.cancel')}
          </Button>
          {changed.length > 0 ? (
            <Button variant="secondary" loading={resume.isPending && resume.variables === 'restart'} onClick={() => go('restart')}>
              {t('monitor.restartNew')}
            </Button>
          ) : null}
          <Button loading={resume.isPending && resume.variables === 'original'} onClick={() => go('original')}>
            {changed.length > 0 ? t('monitor.resumeOriginal') : t('monitor.resumeContinue')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
