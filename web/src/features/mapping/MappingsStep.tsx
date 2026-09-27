import { ArrowRight, Table2, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useOutletContext } from 'react-router'
import { AsyncBoundary } from '@/components/AsyncBoundary'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { EmptyState } from '@/components/EmptyState'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Can } from '@/features/auth/Can'
import type { Project } from '@/features/projects/api'
import { useDeleteMapping, useMappings } from './api'
import { ScoreRing } from './ScoreRing'
import type { MappingSummary } from './types'

const statusTone = { ready: 'success', draft: 'warning', stale: 'danger' } as const

/** Step 4 (OC-035): the project's mappings, each opening its workbench. */
export function MappingsStep() {
  const { t } = useTranslation()
  const project = useOutletContext<Project>()
  const mappings = useMappings(project.id)
  const del = useDeleteMapping(project.id)
  const [removing, setRemoving] = useState<MappingSummary | null>(null)
  return (
    <div className="flex flex-col gap-5 px-8 py-7">
      <div className="flex flex-col gap-1.5">
        <h2 className="text-lg font-semibold">{t('mappings.title')}</h2>
        <p className="text-[13px] text-muted">{t('mappings.intro')}</p>
      </div>
      <AsyncBoundary
        query={mappings}
        isEmpty={(ms) => ms.length === 0}
        empty={
          <EmptyState
            icon={<Table2 />}
            title={t('mappings.emptyTitle')}
            body={t('mappings.emptyBody')}
            action={
              <Button asChild>
                <Link to={`/projects/${project.id}/tables`}>{t('mappings.pickTables')}</Link>
              </Button>
            }
          />
        }
      >
        {(ms) => (
          <ul className="grid grid-cols-1 gap-3 lg:grid-cols-2">
            {ms.map((m) => {
              const auto = m.automapStats ?? m.stats
              return (
                <li key={m.id} className="flex items-center gap-4 rounded-[var(--radius-card)] border border-border bg-surface p-4">
                  <ScoreRing ratio={auto.autoMapRatio} label={t('mappings.autoScore', { pct: Math.round(auto.autoMapRatio * 100) })} />
                  <div className="flex min-w-0 flex-1 flex-col gap-1">
                    <span className="truncate text-[13px] font-medium">
                      {m.sourceSchema}.{m.sourceTable}
                      <span className="px-1.5 text-subtle">→</span>
                      <span className="font-mono text-xs">
                        {m.target.bucket}.{m.target.scope}.{m.target.collection}
                      </span>
                    </span>
                    <span className="flex flex-wrap items-center gap-2 text-xs text-muted">
                      <Badge tone={statusTone[m.status]}>{t(`mappings.status.${m.status}`)}</Badge>
                      {t('mappings.coverage', { pct: Math.round(m.stats.coverageRatio * 100) })}
                      {m.target.createIfMissing ? <Badge tone="primary">{t('targets.willBeCreated')}</Badge> : null}
                    </span>
                  </div>
                  <Can role="editor">
                    <Button variant="ghost" size="icon" aria-label={t('mappings.delete', { table: m.sourceTable })} onClick={() => setRemoving(m)}>
                      <Trash2 aria-hidden />
                    </Button>
                  </Can>
                  <Button variant="secondary" size="sm" asChild>
                    <Link to={`/projects/${project.id}/mappings/${m.id}`} aria-label={t('mappings.openFor', { table: m.sourceTable })}>
                      {t('mappings.open')}
                      <ArrowRight aria-hidden />
                    </Link>
                  </Button>
                </li>
              )
            })}
          </ul>
        )}
      </AsyncBoundary>
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(o) => !o && setRemoving(null)}
        title={t('mappings.deleteTitle')}
        description={t('mappings.deleteBody', { table: removing?.sourceTable ?? '' })}
        confirmLabel={t('mappings.deleteConfirm')}
        destructive
        onConfirm={() => removing && del.mutate(removing.id, { onSettled: () => setRemoving(null) })}
      />
    </div>
  )
}
