import { Activity } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { AsyncBoundary } from '@/components/AsyncBoundary'
import { EmptyState } from '@/components/EmptyState'
import { PageHeader } from '@/components/PageHeader'
import { Badge } from '@/components/ui/badge'
import { useProjects } from '@/features/projects/api'
import { formatDuration, formatRate, humanizeCount, relativeTime } from '@/lib/format'
import { cn } from '@/lib/utils'
import { isTerminal, useJobs, type Job } from './api'
import { JobStatusBadge } from './JobStatusBadge'

function duration(j: Job): string {
  if (!j.startedAt) return '—'
  const end = j.finishedAt ? new Date(j.finishedAt) : new Date()
  return formatDuration((end.getTime() - new Date(j.startedAt).getTime()) / 1000)
}

/** Live rate while a job runs; once it has finished, the average over the run (as on the monitor page). */
function rate(j: Job): number | null {
  if (!j.startedAt) return null
  if (!isTerminal(j.status)) return j.progress.throughput
  const secs = j.progress.elapsedSeconds || ((j.finishedAt ? new Date(j.finishedAt).getTime() : Date.now()) - new Date(j.startedAt).getTime()) / 1000
  return j.progress.written / Math.max(1, secs)
}

/** Jobs across every project (OC-023): status, project, started, duration, written, errors, throughput. */
export function JobsPage() {
  const { t } = useTranslation()
  const jobs = useJobs()
  const projects = useProjects()
  const name = (id: string) => projects.data?.find((p) => p.id === id)?.name ?? '…'
  return (
    <div className="flex flex-col gap-6 px-10 py-8">
      <PageHeader title={t('jobs.title')} description={t('jobs.description')} />
      <AsyncBoundary query={jobs} isEmpty={(js) => js.length === 0} empty={<EmptyState icon={<Activity />} title={t('jobs.emptyTitle')} body={t('jobs.emptyBody')} />}>
        {(js) => (
          <div className="overflow-auto rounded-[var(--radius-card)] border border-border">
            <table className="w-full border-collapse text-[13px]">
              <thead className="bg-surface-muted text-left text-xs text-muted">
                <tr>
                  {['status', 'project', 'started', 'duration', 'written', 'errors', 'throughput'].map((c) => (
                    <th key={c} scope="col" className="px-3 py-2 font-medium">{t(`jobs.col.${c}`)}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {js.map((j) => {
                  const r = rate(j)
                  return (
                    <tr key={j.id} className="border-t border-border hover:bg-surface-muted/50">
                      <td className="px-3 py-2">
                        <span className="flex items-center gap-1.5">
                          <JobStatusBadge status={j.status} />
                          {j.origin === 'external' ? <Badge tone="primary" title={t('jobs.externalHint')}>{t('jobs.external')}</Badge> : null}
                        </span>
                      </td>
                      <td className="px-3 py-2">
                        <Link to={`/projects/${j.projectId}/jobs/${j.id}`} className="font-medium text-primary-soft-foreground hover:underline">
                          {name(j.projectId)}
                        </Link>
                      </td>
                      <td className="px-3 py-2 text-muted">{j.startedAt ? relativeTime(j.startedAt) : t('jobs.notStarted')}</td>
                      <td className="px-3 py-2 font-mono text-xs tabular-nums">{duration(j)}</td>
                      <td className="px-3 py-2 font-mono text-xs tabular-nums">{humanizeCount(j.progress.written)}</td>
                      <td className={cn('px-3 py-2 font-mono text-xs tabular-nums', j.progress.failed > 0 && 'text-danger')}>
                        {j.startedAt ? humanizeCount(j.progress.failed) : '—'}
                      </td>
                      <td className="px-3 py-2 font-mono text-xs tabular-nums" title={isTerminal(j.status) ? t('jobs.rateAvg') : undefined}>
                        {r == null ? '—' : t('jobs.rate', { rate: formatRate(r) })}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </AsyncBoundary>
    </div>
  )
}
