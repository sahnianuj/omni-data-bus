import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Tooltip } from '@/components/ui/tooltip'
import { formatDuration } from '@/lib/format'
import { cn } from '@/lib/utils'
import type { JobStatus, ProgressSnapshot } from './api'

/** Bar color per status (OC-028 AC2). */
export const barTone: Record<JobStatus, string> = {
  queued: 'bg-[var(--primary)]', running: 'bg-[var(--primary)]', pausing: 'bg-[var(--primary)]', paused: 'bg-[var(--primary)]',
  cancelling: 'bg-[var(--muted-foreground)]', cancelled: 'bg-[var(--muted-foreground)]',
  completed: 'bg-[var(--success-dot)]', completed_with_errors: 'bg-[var(--warning-dot)]', failed: 'bg-[var(--danger)]',
}

const fmt = new Intl.NumberFormat('en')

/** Screen readers get a new value at most every 10 s, not every tick. */
function useThrottled<T>(value: T, ms: number): T {
  const [v, setV] = useState(value)
  const [at, setAt] = useState(0)
  useEffect(() => {
    const wait = Math.max(0, at + ms - Date.now())
    const id = setTimeout(() => {
      setV(value)
      setAt(Date.now())
    }, wait)
    return () => clearTimeout(id)
  }, [value, ms, at])
  return v
}

/** The main progress bar (OC-028 AC1): width, label, totals, ETA and elapsed. */
export function JobProgressBar({ snapshot, status, compact }: { snapshot: ProgressSnapshot; status: JobStatus; compact?: boolean }) {
  const { t } = useTranslation()
  const pct = Math.min(100, Math.max(0, snapshot.percent))
  const eta = snapshot.etaSeconds != null ? formatDuration(snapshot.etaSeconds) : null
  const spoken = useThrottled(
    eta ? t('monitor.ariaEta', { pct: Math.round(pct), eta }) : t('monitor.aria', { pct: Math.round(pct) }),
    10_000,
  )
  const running = status === 'running'
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center gap-3">
        <div
          role="progressbar"
          aria-label={t('monitor.progress')}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={Math.round(pct)}
          aria-valuetext={spoken}
          data-status={status}
          className={cn('relative flex-1 overflow-hidden rounded-full bg-surface-muted', compact ? 'h-1.5' : 'h-3')}
        >
          <div
            data-testid="progress-fill"
            className={cn('h-full rounded-full transition-[width] duration-[400ms] ease-linear', barTone[status], running && !compact && 'odb-stripes')}
            style={{ width: `${pct}%` }}
          />
        </div>
        {!compact ? <span className="w-16 text-right font-mono text-sm font-semibold tabular-nums">{t('monitor.pct', { pct: pct.toFixed(1) })}</span> : null}
      </div>
      {!compact ? (
        <div className="flex flex-wrap items-center justify-between gap-2 text-[13px] text-muted">
          <span className="tabular-nums">
            {t('monitor.totals', { processed: fmt.format(snapshot.processed), total: fmt.format(snapshot.total) })}{' '}
            {snapshot.totalKind === 'exact' ? (
              t('monitor.exact')
            ) : (
              <Tooltip content={t('monitor.estimateHint')}>
                <span tabIndex={0} className="underline decoration-dotted underline-offset-2">
                  {t('monitor.estimate')}
                </span>
              </Tooltip>
            )}
          </span>
          <span className="tabular-nums">
            {eta ? `${t('monitor.eta', { eta })} · ` : ''}
            {t('monitor.elapsed', { elapsed: formatDuration(snapshot.elapsedSeconds) })}
          </span>
        </div>
      ) : null}
    </div>
  )
}
