import { Bell } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate, useParams } from 'react-router'
import { AsyncBoundary } from '@/components/AsyncBoundary'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { formatBytes, formatRate, humanizeCount } from '@/lib/format'
import { useJobEvents, type Connection } from '@/lib/sse/useJobEvents'
import { cn } from '@/lib/utils'
import { isTerminal, useJob, useJobLog, type ErrorSample, type Job } from './api'
import { JobControls } from './JobControls'
import { JobErrorsTab } from './JobErrorsTab'
import { JobProgressBar } from './JobProgressBar'
import { JobStatusBadge } from './JobStatusBadge'
import { JobSummaryCard } from './JobSummaryCard'
import { JobTimeline } from './JobTimeline'
import { addSamples, LiveErrorFeed } from './LiveErrorFeed'
import { MappingProgressList } from './MappingProgressList'
import { OutputBrowser } from './OutputBrowser'
import { failedTone, StatTile } from './StatTile'
import { pushPoint, ThroughputSparkline } from './ThroughputSparkline'

const APP = 'Omni-Data-Bus'
const fmt = new Intl.NumberFormat('en')

function LiveDot({ connection, terminal }: { connection: Connection; terminal: boolean }) {
  const { t } = useTranslation()
  if (terminal) return null
  const live = connection === 'live'
  return (
    <span role="status" className={cn('flex items-center gap-1.5 text-xs', live ? 'text-success' : 'text-warning')}>
      <span className={cn('size-2 rounded-full', live ? 'bg-[var(--success-dot)]' : 'animate-pulse bg-[var(--warning-dot)]')} aria-hidden />
      {live ? t('monitor.live') : t('monitor.reconnecting')}
    </span>
  )
}

/** /projects/:projectId/jobs/:jobId — the live migration monitor (OC-028). */
export function JobMonitorPage() {
  const { jobId = '' } = useParams()
  const job = useJob(jobId)
  return (
    <div className="flex flex-col gap-5 px-8 py-7">
      <AsyncBoundary query={job}>{(j) => <Monitor job={j} />}</AsyncBoundary>
    </div>
  )
}

function Monitor({ job }: { job: Job }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [tab, setTab] = useState('overview')
  const [feed, setFeed] = useState<ErrorSample[]>([])
  const [spark, setSpark] = useState<number[]>([])
  const [lastSeq, setLastSeq] = useState(-1)
  const terminal = isTerminal(job.status)
  const external = job.origin === 'external' ? job.external : undefined
  const { connection } = useJobEvents(job.id, {
    enabled: !terminal,
    onErrorSample: (s) => setFeed((f) => addSamples(f, [s])),
    onCompleted: (s) => {
      if (typeof Notification !== 'undefined' && Notification.permission === 'granted') {
        new Notification(t('monitor.notifyTitle'), { body: t('monitor.notifyBody', { n: fmt.format(s.written), status: t(`jobStatus.${s.status}`) }) })
      }
    },
  })
  const log = useJobLog(job.id, tab === 'log')
  const p = job.progress

  // One sparkline point per new snapshot (adjusting state while rendering, not in an effect).
  if (p.seq !== lastSeq) {
    setLastSeq(p.seq)
    if (!terminal) setSpark((pts) => pushPoint(pts, p.throughput))
  }

  // The tab title follows progress: "33% · Omni-Data-Bus", then "✓ Done · …".
  useEffect(() => {
    document.title = terminal
      ? `${job.status === 'completed' || job.status === 'completed_with_errors' ? t('monitor.titleDone') : t(`jobStatus.${job.status}`)} · ${APP}`
      : `${Math.floor(p.percent)}% · ${APP}`
    return () => {
      document.title = APP
    }
  }, [terminal, job.status, p.percent, t])

  const maxErrors = job.options.errorPolicy?.maxErrors
  const ratio = p.processed > 0 ? (p.failed / p.processed) * 100 : 0
  const canNotify = typeof Notification !== 'undefined' && Notification.permission === 'default' && !terminal
  return (
    <>
      <header className="flex flex-wrap items-center gap-3">
        <h2 className="text-lg font-semibold">{t('monitor.heading', { id: job.id.slice(-6).toUpperCase() })}</h2>
        <JobStatusBadge status={job.status} />
        {job.origin === 'external' ? <Badge tone="primary" title={t('jobs.externalHint')}>{t('jobs.external')}</Badge> : null}
        <LiveDot connection={connection} terminal={terminal} />
        <div className={cn('ml-auto flex items-center gap-2', job.origin === 'external' && 'hidden')}>
          {canNotify ? (
            <Button variant="ghost" size="sm" onClick={() => void Notification.requestPermission()}>
              <Bell aria-hidden />
              {t('monitor.notifyMe')}
            </Button>
          ) : null}
          <JobControls job={job} onRetried={(id) => void navigate(`/projects/${job.projectId}/jobs/${id}`)} />
        </div>
      </header>
      <p className="text-[13px] text-muted">{t(terminal ? 'monitor.subtitleDone' : 'monitor.subtitle', { count: p.mappings.length })}</p>
      {external ? (
        <section aria-label={t('monitor.external.title')} className="flex flex-col gap-1 rounded-[var(--radius-card)] border border-primary/30 bg-primary-soft px-4 py-3 text-[13px]">
          <p>{t('monitor.external.body', { version: external.odb.version, run: external.runId.slice(0, 8), export: external.bundleSha256.slice(0, 12) })}</p>
          <p className="text-xs text-muted">
            {t('monitor.external.exitCode', { code: external.exitCode })} · {t('monitor.external.errors', { count: external.errors.total })}
          </p>
        </section>
      ) : terminal ? (
        <JobSummaryCard job={job} onViewErrors={() => setTab('errors')} />
      ) : null}
      <JobProgressBar snapshot={p} status={job.status} />
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <StatTile
          label={terminal ? t('monitor.avgThroughput') : t('monitor.throughput')}
          value={t('jobs.rate', { rate: formatRate(terminal ? p.written / Math.max(1, p.elapsedSeconds) : p.throughput) })}
          sub={t('monitor.peak', { rate: formatRate(p.peakThroughput) })}
        >
          <ThroughputSparkline points={spark} />
        </StatTile>
        <StatTile label={t('monitor.written')} value={fmt.format(p.written)} sub={formatBytes(p.bytesWritten)} />
        <StatTile label={t('monitor.skipped')} value={fmt.format(p.skipped)} />
        <StatTile
          label={t('monitor.failed')}
          value={fmt.format(p.failed)}
          tone={failedTone(p.failed, maxErrors)}
          sub={
            p.failed > 0 ? (
              <button type="button" onClick={() => setTab('errors')} className="text-primary-soft-foreground hover:underline">
                {t('monitor.failedSub', { ratio: ratio.toFixed(2) })}
              </button>
            ) : null
          }
        />
      </div>
      <Tabs value={tab} onValueChange={setTab} className="flex flex-col gap-4">
        <TabsList>
          <TabsTrigger value="overview">{t('monitor.tab.overview')}</TabsTrigger>
          <TabsTrigger value="tables">{t('monitor.tab.tables')}</TabsTrigger>
          <TabsTrigger value="errors">
            {t('monitor.tab.errors')}
            {p.failed ? <span className="ml-1 text-subtle">{humanizeCount(p.failed)}</span> : null}
          </TabsTrigger>
          <TabsTrigger value="options">{t('monitor.tab.options')}</TabsTrigger>
          <TabsTrigger value="log">{t('monitor.tab.log')}</TabsTrigger>
          {terminal && !external ? <TabsTrigger value="output">{t('monitor.tab.output')}</TabsTrigger> : null}
        </TabsList>
        <TabsContent value="overview" className="flex flex-col gap-5">
          <MappingProgressList projectId={job.projectId} mappings={p.mappings} />
          <LiveErrorFeed samples={feed} />
        </TabsContent>
        <TabsContent value="tables">
          <MappingProgressList projectId={job.projectId} mappings={p.mappings} />
        </TabsContent>
        <TabsContent value="errors">
          <JobErrorsTab job={job} />
        </TabsContent>
        <TabsContent value="options">
          <dl className="grid max-w-2xl grid-cols-[auto_1fr] gap-x-6 gap-y-1.5 text-[13px]">
            {Object.entries(job.options)
              .filter(([k]) => k !== 'mappingIds')
              .map(([k, v]) => (
                <div key={k} className="contents">
                  <dt className="text-muted">{k}</dt>
                  <dd className="font-mono text-xs">{typeof v === 'object' ? JSON.stringify(v) : String(v ?? '—')}</dd>
                </div>
              ))}
          </dl>
        </TabsContent>
        <TabsContent value="log">
          <AsyncBoundary query={log}>{(evs) => <JobTimeline events={evs} />}</AsyncBoundary>
        </TabsContent>
        {terminal ? (
          <TabsContent value="output">
            <OutputBrowser job={job} mappings={p.mappings.map((m) => ({ mappingId: m.mappingId, table: m.table }))} />
          </TabsContent>
        ) : null}
      </Tabs>
    </>
  )
}
