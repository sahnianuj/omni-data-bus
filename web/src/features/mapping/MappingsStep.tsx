import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { AlertTriangle, ArrowRight, ChevronDown, Table2, Trash2 } from 'lucide-react'
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
import { formatLocation } from '@/features/targets/kinds'
import { BAR_VIEWS } from './viewPrefs'

const statusTone = { ready: 'success', draft: 'warning', stale: 'danger' } as const

/** Step 4 (OC-035): the project's mappings, each opening its workbench. */
/** The document at a glance (OC-062): flat, or what nests in it, and grouping. */
function useShapeText() {
  const { t } = useTranslation()
  return (m: MappingSummary) => {
    const sh = m.shape
    const parts: string[] = []
    if (sh?.arrays) parts.push(t('mappings.shape.arrays', { count: sh.arrays }))
    if (sh?.objects) parts.push(t('mappings.shape.objects', { count: sh.objects }))
    const nested = parts.length ? t('mappings.shape.nested', { parts: parts.join(', ') }) : t('mappings.shape.flat')
    return sh?.groupBy?.length ? `${nested} · ${t('mappings.shape.grouped', { columns: sh.groupBy.join(', ') })}` : nested
  }
}

export function MappingsStep() {
  const { t } = useTranslation()
  const project = useOutletContext<Project>()
  const mappings = useMappings(project.id)
  const del = useDeleteMapping(project.id)
  const [removing, setRemoving] = useState<MappingSummary | null>(null)
  const shapeText = useShapeText()
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
                      {m.mode === 'composed' ? `${m.driving.schema}.${m.driving.name}` : `${m.sourceSchema}.${m.sourceTable}`}
                      <span className="px-1.5 text-subtle">→</span>
                      <span className="font-mono text-xs">
                        {formatLocation(m.location)}
                      </span>
                    </span>
                    <span className="flex flex-wrap items-center gap-2 text-xs text-muted">
                      {/* Edits save automatically: "draft" only means errors are left to fix (OC-083). */}
                      <Badge tone={statusTone[m.status]} title={m.status === 'ready' ? undefined : t('mappings.statusHint')}>
                        {m.status === 'draft' && m.errors ? t('mappings.toFix', { count: m.errors }) : t(`mappings.status.${m.status}`)}
                      </Badge>
                      {m.mode === 'composed' ? <Badge tone="primary">{t('composer.badge')}</Badge> : null}
                      {t('mappings.coverage', { pct: Math.round(m.stats.coverageRatio * 100) })}
                      {m.target.createIfMissing ? <Badge tone="primary">{t('targets.willBeCreated')}</Badge> : null}
                    </span>
                    <span className="text-xs text-muted" data-testid="shape">
                      {shapeText(m)}
                    </span>
                    {m.firstError ? (
                      <span className="flex min-w-0 items-center gap-1.5 text-xs text-warning">
                        <AlertTriangle className="size-3.5 shrink-0" aria-hidden />
                        <span className="truncate" title={m.firstError.message}>
                          {m.errors > 1 ? t('mappings.firstErrorMore', { message: m.firstError.message, count: m.errors - 1 }) : m.firstError.message}
                        </span>
                        <Link to={`/projects/${project.id}/mappings/${m.id}?panel=issues`} className="shrink-0 font-medium text-primary hover:underline" aria-label={t('mappings.fixFor', { table: m.sourceTable })}>
                          {t('mappings.fix')}
                        </Link>
                      </span>
                    ) : null}
                  </div>
                  <Can role="editor">
                    <Button variant="ghost" size="icon" aria-label={t('mappings.delete', { table: m.sourceTable })} onClick={() => setRemoving(m)}>
                      <Trash2 aria-hidden />
                    </Button>
                  </Can>
                  <span className="flex items-center">
                    <Button variant="secondary" size="sm" className="rounded-r-none" asChild>
                      <Link to={`/projects/${project.id}/mappings/${m.id}`} aria-label={t('mappings.openFor', { table: m.sourceTable })}>
                        {t('mappings.open')}
                        <ArrowRight aria-hidden />
                      </Link>
                    </Button>
                    <DropdownMenu.Root>
                      <DropdownMenu.Trigger asChild>
                        <Button variant="secondary" size="sm" className="rounded-l-none border-l border-border px-1.5" aria-label={t('mappings.openIn', { table: m.sourceTable })}>
                          <ChevronDown aria-hidden />
                        </Button>
                      </DropdownMenu.Trigger>
                      <DropdownMenu.Portal>
                        <DropdownMenu.Content align="end" className="z-50 min-w-48 rounded-md border border-border bg-surface p-1 shadow-lg">
                          {BAR_VIEWS.map((v) => (
                            <DropdownMenu.Item key={v} asChild>
                              <Link to={`/projects/${project.id}/mappings/${m.id}?view=${v}`} className="block rounded px-2 py-1.5 text-[13px] outline-none hover:bg-surface-muted focus:bg-surface-muted">
                                {t('mappings.openInView', { view: t(`workbench.mode.${v}`) })}
                              </Link>
                            </DropdownMenu.Item>
                          ))}
                        </DropdownMenu.Content>
                      </DropdownMenu.Portal>
                    </DropdownMenu.Root>
                  </span>
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
