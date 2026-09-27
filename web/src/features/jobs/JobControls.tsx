import { Loader2, Pause, Play, RotateCcw, X } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/button'
import { Can } from '@/features/auth/Can'
import { isTerminal, resumeNeedsDialog, useJobControl, useRetryJob, type Job } from './api'
import { ResumeDialog } from './ResumeDialog'

/**
 * Pause, resume, cancel and the follow-up actions for the job's status (OC-028 AC2, AC4).
 * onRetried opens a job that replaced this one (retry of failed rows, or a restart).
 */
export function JobControls({ job, onRetried }: { job: Job; onRetried?: (id: string) => void }) {
  const { t } = useTranslation()
  const control = useJobControl(job.id, job.projectId)
  const retry = useRetryJob(job.id, job.projectId)
  const [confirmCancel, setConfirmCancel] = useState(false)
  const [confirmResume, setConfirmResume] = useState(false)
  // Pause is optimistic: "Pausing…" shows from the click until the status arrives.
  const [pausingFrom, setPausingFrom] = useState<string | null>(null)
  const pausing = job.status === 'pausing' || (pausingFrom !== null && pausingFrom === job.status)
  const s = job.status

  const cancel = (
    <Can role="editor">
      <Button variant="secondary" size="sm" onClick={() => setConfirmCancel(true)}>
        <X aria-hidden />
        {t('monitor.cancel')}
      </Button>
    </Can>
  )
  return (
    <div className="flex flex-wrap items-center gap-2">
      {s === 'queued' ? <span className="text-xs text-muted">{t('monitor.waiting')}</span> : null}
      {s === 'running' || s === 'pausing' ? (
        pausing ? (
          <Button variant="secondary" size="sm" disabled>
            <Loader2 className="animate-spin" aria-hidden />
            {t('monitor.pausing')}
          </Button>
        ) : (
          <Can role="editor">
            <Button
              variant="secondary"
              size="sm"
              onClick={() => {
                setPausingFrom(job.status)
                control.mutate('pause', { onError: () => setPausingFrom(null) })
              }}
            >
              <Pause aria-hidden />
              {t('monitor.pause')}
            </Button>
          </Can>
        )
      ) : null}
      {s === 'paused' || (s === 'failed' && job.resumable) ? (
        <Can role="editor">
          <Button
            size="sm"
            loading={control.isPending && control.variables === 'resume'}
            onClick={() => (resumeNeedsDialog(job.resumeInfo) ? setConfirmResume(true) : control.mutate('resume'))}
          >
            <Play aria-hidden />
            {t('monitor.resume')}
          </Button>
        </Can>
      ) : null}
      {s === 'failed' && job.retryable ? (
        <Can role="editor">
          <Button variant="secondary" size="sm" loading={retry.isPending} onClick={() => retry.mutate(undefined, { onSuccess: (r) => onRetried?.(r.job.id) })}>
            <RotateCcw aria-hidden />
            {t('errors.retry')}
          </Button>
        </Can>
      ) : null}
      {s === 'queued' || s === 'running' || s === 'paused' ? cancel : null}
      {isTerminal(s) ? (
        <Button variant="secondary" size="sm" asChild>
          <Link to={`/projects/${job.projectId}/review`}>{t('monitor.runAgain')}</Link>
        </Button>
      ) : null}
      {control.error ? <ErrorState error={control.error} /> : null}
      <ResumeDialog
        job={job}
        open={confirmResume}
        onOpenChange={setConfirmResume}
        onResumed={(j, mode) => {
          if (mode === 'restart') onRetried?.(j.id)
        }}
      />
      <ConfirmDialog
        open={confirmCancel}
        onOpenChange={setConfirmCancel}
        title={t('monitor.cancelTitle')}
        description={t('monitor.cancelBody')}
        confirmLabel={t('monitor.cancelConfirm')}
        destructive
        loading={control.isPending}
        onConfirm={() => control.mutate('cancel', { onSettled: () => setConfirmCancel(false) })}
      />
    </div>
  )
}
