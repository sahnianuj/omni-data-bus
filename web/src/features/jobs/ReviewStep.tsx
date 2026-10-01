import { ArrowRight, Play, ShieldCheck } from 'lucide-react'
import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useNavigate, useOutletContext } from 'react-router'
import { AsyncBoundary } from '@/components/AsyncBoundary'
import { ErrorState } from '@/components/ErrorState'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Can } from '@/features/auth/Can'
import { useMappings } from '@/features/mapping/api'
import type { Project } from '@/features/projects/api'
import { humanizeCount } from '@/lib/format'
import { formatLocation, useKindCapabilities } from '@/features/targets/kinds'
import { AdvancedOptions } from './AdvancedOptions'
import { defaultJobOptions, useCreateJob, usePreflight, type JobOptions, type PreflightResult } from './api'
import { PreflightChecklist } from './PreflightChecklist'

/** Step 5 (OC-023): what will be migrated, advanced options, pre-flight, and Migrate. */
export function ReviewStep() {
  const { t } = useTranslation()
  const project = useOutletContext<Project>()
  const caps = useKindCapabilities(project.target?.type)
  const mappings = useMappings(project.id)
  const navigate = useNavigate()
  const [options, setOptions] = useState<JobOptions | null>(defaultJobOptions)
  const [confirm, setConfirm] = useState<PreflightResult | null>(null)
  const preflight = usePreflight(project.id)
  const create = useCreateJob(project.id)
  const onOptions = useCallback((o: JobOptions | null) => setOptions(o), [])
  const active = project.latestJob && !['cancelled', 'completed', 'completed_with_errors', 'failed'].includes(project.latestJob.status)

  const check = () => options && preflight.mutate(options)
  const migrate = () =>
    options &&
    preflight.mutate(options, {
      onSuccess: (res) => {
        if (res.ok) setConfirm(res)
      },
    })
  const start = () =>
    options &&
    create.mutate(options, {
      onSuccess: (res) => {
        setConfirm(null)
        void navigate(`/projects/${project.id}/jobs/${res.job.id}`)
      },
      // A check that started failing after confirm comes back as PREFLIGHT_FAILED naming it.
      onError: () => setConfirm(null),
    })

  const result = preflight.data
  const warnings = confirm?.checks.filter((c) => c.status === 'warn') ?? []
  return (
    <div className="flex flex-col gap-5 px-8 py-7">
      <div className="flex flex-col gap-1.5">
        <h2 className="text-lg font-semibold">{t('review.title')}</h2>
        <p className="max-w-3xl text-[13px] text-muted">{t('review.intro')}</p>
      </div>
      {active ? (
        <div role="status" className="flex items-center gap-3 rounded-[var(--radius-card)] border border-primary/30 bg-primary-soft px-4 py-3 text-[13px]">
          <span className="flex-1">{t('review.activeJob')}</span>
          <Button size="sm" asChild>
            <Link to={`/projects/${project.id}/jobs/${project.latestJob!.id}`}>
              {t('review.openJob')}
              <ArrowRight aria-hidden />
            </Link>
          </Button>
        </div>
      ) : null}
      <AsyncBoundary query={mappings}>
        {(ms) => {
          const ready = ms.filter((m) => m.status === 'ready')
          return (
            <section aria-label={t('review.tables')} className="overflow-hidden rounded-[var(--radius-card)] border border-border">
              <table className="w-full border-collapse text-[13px]">
                <thead className="bg-surface-muted text-left text-xs text-muted">
                  <tr>
                    <th scope="col" className="px-3 py-2 font-medium">{t('review.table')}</th>
                    <th scope="col" className="px-3 py-2 font-medium">{t('review.target')}</th>
                    <th scope="col" className="px-3 py-2 font-medium">{t('review.status')}</th>
                  </tr>
                </thead>
                <tbody>
                  {ms.map((m) => (
                    <tr key={m.id} className="border-t border-border">
                      <td className="px-3 py-2 font-medium">{m.sourceSchema}.{m.sourceTable}</td>
                      <td className="px-3 py-2 font-mono text-xs">{formatLocation(m.location)}</td>
                      <td className="px-3 py-2">
                        {m.status === 'ready' ? <Badge tone="success">{t('review.willMigrate')}</Badge> : <Badge tone="warning">{t('review.notReady')}</Badge>}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <p className="border-t border-border px-3 py-2 text-xs text-muted">{t('review.readyCount', { ready: ready.length, count: ms.length })}</p>
            </section>
          )
        }}
      </AsyncBoundary>
      <AdvancedOptions onChange={onOptions} capabilities={caps} />
      {preflight.error ? <ErrorState error={preflight.error} /> : null}
      {create.error ? <ErrorState error={create.error} /> : null}
      {result ? <PreflightChecklist result={result} /> : null}
      {/* Each action is wrapped on its own: Can disables its direct child, and a footer can't be disabled. */}
      <footer className="sticky bottom-0 flex h-16 items-center justify-end gap-2 border-t border-border bg-surface px-1">
        <Can role="editor">
          <Button variant="secondary" disabled={!options} loading={preflight.isPending && !confirm} onClick={check}>
            <ShieldCheck aria-hidden />
            {t('review.runChecks')}
          </Button>
        </Can>
        <Can role="editor">
          <Button disabled={!options || !!active || (result ? !result.ok : false)} loading={preflight.isPending || create.isPending} onClick={migrate}>
            <Play aria-hidden />
            {t('review.migrate')}
          </Button>
        </Can>
      </footer>

      <Dialog open={confirm !== null} onOpenChange={(o) => !o && setConfirm(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('review.confirmTitle')}</DialogTitle>
            <DialogDescription>{t('review.confirmBody')}</DialogDescription>
          </DialogHeader>
          {confirm ? (
            <div className="flex flex-col gap-3 text-[13px]">
              <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5">
                <dt className="text-muted">{t('review.rows')}</dt>
                <dd className="font-mono">≈ {humanizeCount(confirm.totalRows)}</dd>
                <dt className="text-muted">{t('review.writeMode')}</dt>
                <dd>{t(`jobOptions.${options?.writeMode ?? 'upsert'}`)}</dd>
                <dt className="text-muted">{t('review.targets')}</dt>
                <dd className="flex flex-col font-mono text-xs">
                  {confirm.mappings.map((m) => (
                    <span key={m.mappingId}>
                      {m.table} → {m.target.bucket}.{m.target.scope}.{m.target.collection}
                    </span>
                  ))}
                </dd>
              </dl>
              {warnings.length ? (
                <ul aria-label={t('review.warnings')} className="flex flex-col gap-1 rounded-lg bg-warning-soft p-3 text-warning">
                  {warnings.map((w) => (
                    <li key={w.id}>
                      {w.label}
                      {w.detail ? `: ${w.detail}` : ''}
                    </li>
                  ))}
                </ul>
              ) : null}
            </div>
          ) : null}
          <DialogFooter>
            <Button variant="ghost" onClick={() => setConfirm(null)}>
              {t('common.cancel')}
            </Button>
            <Button loading={create.isPending} onClick={start}>
              <Play aria-hidden />
              {t('review.start')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
