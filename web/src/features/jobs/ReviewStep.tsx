import { ArrowRight, Download, Play, ShieldCheck } from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useNavigate, useOutletContext } from 'react-router'
import { AsyncBoundary } from '@/components/AsyncBoundary'
import { ErrorState } from '@/components/ErrorState'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useSession } from '@/features/auth/api'
import { Can, hasRole } from '@/features/auth/Can'
import { useMappings } from '@/features/mapping/api'
import type { Project } from '@/features/projects/api'
import { humanizeCount } from '@/lib/format'
import { formatLocation, useKindCapabilities } from '@/features/targets/kinds'
import { AdvancedOptions } from './AdvancedOptions'
import { defaultJobOptions, useCreateJob, usePreflight, type JobOptions } from './api'
import { PreflightChecklist } from './PreflightChecklist'

/**
 * Step 5 (OC-023): what will be migrated, advanced options, pre-flight, and Migrate. The checks run
 * on their own when the step opens and when an option changes, so anything that needs attention
 * shows here before Migrate, which then starts the job without asking again (OC-078).
 */
export function ReviewStep() {
  const { t } = useTranslation()
  const project = useOutletContext<Project>()
  const caps = useKindCapabilities(project.target?.type)
  const mappings = useMappings(project.id)
  const navigate = useNavigate()
  const editor = hasRole(useSession().data?.role, 'editor')
  const [options, setOptions] = useState<JobOptions | null>(defaultJobOptions)
  const preflight = usePreflight(project.id)
  const create = useCreateJob(project.id)
  const onOptions = useCallback((o: JobOptions | null) => setOptions(o), [])
  const active = project.latestJob && !['cancelled', 'completed', 'completed_with_errors', 'failed'].includes(project.latestJob.status)

  const { mutate: runChecks } = preflight
  const key = options ? JSON.stringify(options) : ''
  // Check again shortly after the options settle (typing a batch size doesn't run one per key).
  useEffect(() => {
    if (!key || active || !editor) return
    const id = setTimeout(() => runChecks(JSON.parse(key) as JobOptions), 400)
    return () => clearTimeout(id)
  }, [key, active, editor, runChecks])

  const check = () => options && runChecks(options)
  const migrate = () =>
    options &&
    create.mutate(options, {
      onSuccess: (res) => void navigate(`/projects/${project.id}/jobs/${res.job.id}`),
      // The server checks again; a check that started failing comes back as PREFLIGHT_FAILED, and
      // the checklist is refreshed to show which.
      onError: () => runChecks(options),
    })

  const result = preflight.data
  // A result for other options than the current ones is out of date until the next check.
  const current = !!result && !preflight.isPending && JSON.stringify(preflight.variables) === key
  const ready = current && result.ok
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
        {ready ? (
          <p className="mr-auto text-[13px] text-muted">
            {t('review.summary', { rows: humanizeCount(result.totalRows), mode: t(`jobOptions.${options?.writeMode ?? 'upsert'}`) })}
          </p>
        ) : null}
        <Button variant="brand" asChild title={t('review.exportHint')}>
          <Link to={`/projects/${project.id}/export`}>
            <Download aria-hidden />
            {t('review.export')}
          </Link>
        </Button>
        <Can role="editor">
          <Button variant="secondary" disabled={!options} loading={preflight.isPending} onClick={check}>
            <ShieldCheck aria-hidden />
            {t('review.runChecks')}
          </Button>
        </Can>
        <Can role="editor">
          <Button disabled={!ready || !!active} loading={create.isPending} onClick={migrate}>
            <Play aria-hidden />
            {t('review.migrate')}
          </Button>
        </Can>
      </footer>
    </div>
  )
}
