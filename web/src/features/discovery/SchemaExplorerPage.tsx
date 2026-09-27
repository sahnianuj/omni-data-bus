import { ArrowLeft, ArrowRight, DatabaseZap, Plus, RefreshCw, Search, TableProperties } from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useOutletContext, useSearchParams } from 'react-router'
import { toast } from 'sonner'
import { EmptyState } from '@/components/EmptyState'
import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Can, hasRole } from '@/features/auth/Can'
import { useSession } from '@/features/auth/api'
import { useMappings } from '@/features/mapping/api'
import type { Project } from '@/features/projects/api'
import { relativeTime } from '@/lib/format'
import { cn } from '@/lib/utils'
import { useDiscover, useSchema, type SchemaDiff, type Snapshot } from './api'
import { DiffSheet } from './DiffSheet'
import { SchemaTree } from './SchemaTree'
import { TableDetail } from './TableDetail'
import { chips, schemaId, tableId, type Chip } from './tree'
import { useSchemaSearch } from './useSchemaSearch'

const MIN_TREE = 220
const MAX_TREE = 520

/** /projects/:projectId/source/schema — browse the project's source schema (OC-011). */
export function SchemaExplorerPage() {
  const { t } = useTranslation()
  const project = useOutletContext<Project>()
  const connId = project.source?.id ?? ''
  const schema = useSchema(connId)
  const discover = useDiscover(connId)
  const session = useSession()
  const canEdit = hasRole(session.data?.role, 'editor')
  const [diff, setDiff] = useState<SchemaDiff | null>(null)
  const [diffOpen, setDiffOpen] = useState(false)
  const autoStarted = useRef(false)

  const runDiscovery = (announce: boolean) =>
    discover.mutate(undefined, {
      onSuccess: (st) => {
        if (!announce) return
        const d = st.diff
        const n = d ? d.addedTables.length + d.removedTables.length + d.changedTables.length : 0
        if (d && n > 0) {
          setDiff(d)
          toast.info(t('explorer.changed', { count: n }), { action: { label: t('explorer.viewChanges'), onClick: () => setDiffOpen(true) } })
        } else {
          toast.success(t('explorer.noChanges'))
        }
      },
    })

  // First visit: an editor's source is discovered automatically.
  useEffect(() => {
    if (schema.data === null && canEdit && !autoStarted.current) {
      autoStarted.current = true
      runDiscovery(false)
    }
  })

  const back = (
    <Button variant="ghost" size="sm" asChild>
      <Link to={`/projects/${project.id}/source`}>
        <ArrowLeft aria-hidden />
        {t('explorer.back')}
      </Link>
    </Button>
  )

  if (!project.source) {
    return (
      <div className="px-8 py-7">
        <EmptyState icon={<DatabaseZap />} title={t('explorer.noSourceTitle')} body={t('explorer.noSourceBody')} action={back} />
      </div>
    )
  }
  if (schema.status === 'pending' || (schema.data === null && discover.isPending)) {
    return (
      <div role="status" aria-busy="true" className="flex flex-col gap-3 px-8 py-7">
        <p className="text-sm text-muted">{schema.data === null ? t('explorer.discovering') : t('common.loading')}</p>
        <Skeleton className="h-8 w-1/3" />
        <Skeleton className="h-64 w-full" />
      </div>
    )
  }
  if (schema.status === 'error') {
    return (
      <div className="px-8 py-7">
        <ErrorState error={schema.error} onRetry={() => void schema.refetch()} />
      </div>
    )
  }
  if (schema.data === null) {
    return (
      <div className="flex flex-col gap-4 px-8 py-7">
        {discover.error ? <ErrorState error={discover.error} onRetry={() => runDiscovery(false)} /> : null}
        <EmptyState
          icon={<DatabaseZap />}
          title={t('explorer.notDiscoveredTitle')}
          body={canEdit ? t('explorer.notDiscoveredBody') : t('explorer.notDiscoveredViewer')}
          action={
            canEdit ? (
              <Button loading={discover.isPending} onClick={() => runDiscovery(false)}>
                <DatabaseZap aria-hidden />
                {t('explorer.discover')}
              </Button>
            ) : (
              back
            )
          }
        />
      </div>
    )
  }
  return (
    <>
      <Explorer
        connId={connId}
        snapshot={schema.data}
        back={back}
        refresh={
          canEdit ? (
            <Button variant="secondary" size="sm" loading={discover.isPending} onClick={() => runDiscovery(true)}>
              <RefreshCw aria-hidden />
              {t('explorer.refresh')}
            </Button>
          ) : null
        }
        error={discover.error}
      />
      <DiffSheet diff={diff} open={diffOpen} onOpenChange={setDiffOpen} />
    </>
  )
}

function Explorer({ connId, snapshot, back, refresh, error }: { connId: string; snapshot: Snapshot; back: ReactNode; refresh: ReactNode; error: unknown }) {
  const project = useOutletContext<Project>()
  const { t } = useTranslation()
  const [params, setParams] = useSearchParams()
  const tree = useSchemaSearch(snapshot.schemas)
  const [width, setWidth] = useState(280)
  const drag = useRef<{ x: number; w: number } | null>(null)

  // The selection lives in the URL (?schema=&table=) so it survives refreshes and back/forward.
  const selSchema = params.get('schema') ?? ''
  const selTable = params.get('table') ?? ''
  const selected = useMemo(() => snapshot.schemas.find((s) => s.name === selSchema)?.tables.find((x) => x.name === selTable), [snapshot, selSchema, selTable])
  const select = (schema: string, table: string) => setParams({ schema, table }, { replace: false })
  const { setOpen } = tree
  const navigate = (schema: string, table: string) => {
    setOpen(schemaId(schema), true)
    select(schema, table)
  }

  const total = snapshot.tableCount
  const onResizeKey = (e: KeyboardEvent) => {
    if (e.key === 'ArrowLeft') setWidth((w) => Math.max(MIN_TREE, w - 16))
    else if (e.key === 'ArrowRight') setWidth((w) => Math.min(MAX_TREE, w + 16))
  }
  const onPointerDown = (e: PointerEvent<HTMLDivElement>) => {
    drag.current = { x: e.clientX, w: width }
    e.currentTarget.setPointerCapture(e.pointerId)
  }
  const onPointerMove = (e: PointerEvent<HTMLDivElement>) => {
    if (drag.current) setWidth(Math.min(MAX_TREE, Math.max(MIN_TREE, drag.current.w + e.clientX - drag.current.x)))
  }

  return (
    <div className="flex h-full min-h-[520px] flex-col">
      <div className="flex h-12 shrink-0 items-center gap-3 border-b border-border px-4">
        {back}
        <h2 className="text-sm font-semibold">{t('explorer.title')}</h2>
        <span className="text-xs text-muted">
          {t('explorer.server', { database: snapshot.database, version: snapshot.serverVersion })}
        </span>
        <div className="ml-auto flex items-center gap-2">{refresh}</div>
      </div>
      {error ? (
        <div className="border-b border-border px-4 py-3">
          <ErrorState error={error} />
        </div>
      ) : null}
      <div className="flex min-h-0 flex-1">
        <aside style={{ width }} className="flex shrink-0 flex-col border-r border-border bg-surface">
          <div className="flex flex-col gap-2 border-b border-border p-3">
            <div className="relative">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-subtle" aria-hidden />
              <Input
                value={tree.query}
                onChange={(e) => tree.setQuery(e.target.value)}
                placeholder={t('explorer.search')}
                aria-label={t('explorer.search')}
                className="h-8 pl-8 text-[13px]"
              />
            </div>
            <div className="flex flex-wrap gap-1.5" role="group" aria-label={t('explorer.filters')}>
              {chips.map((c: Chip) => (
                <button
                  key={c}
                  type="button"
                  aria-pressed={tree.active.has(c)}
                  onClick={() => tree.toggleChip(c)}
                  className={cn(
                    'h-6 rounded-full border px-2 text-xs',
                    tree.active.has(c) ? 'border-primary bg-primary-soft text-primary-soft-foreground' : 'border-border text-muted hover:text-foreground',
                  )}
                >
                  {t(`explorer.chip.${c}`)}
                </button>
              ))}
            </div>
          </div>
          {total === 0 ? (
            <p className="p-4 text-[13px] text-muted">{t('explorer.noTables')}</p>
          ) : tree.nodes.length === 0 ? (
            <p className="p-4 text-[13px] text-muted">{t('explorer.noMatches')}</p>
          ) : (
            <SchemaTree
              label={t('explorer.treeLabel')}
              nodes={tree.nodes}
              expanded={tree.expanded}
              setOpen={tree.setOpen}
              highlights={tree.highlights}
              selected={selected ? tableId(selSchema, selTable) : undefined}
              onSelect={select}
            />
          )}
          <footer className="border-t border-border px-3 py-2 text-xs text-subtle">
            {t('explorer.footer', { when: relativeTime(snapshot.capturedAt), count: total })}
          </footer>
        </aside>
        <div
          role="separator"
          aria-orientation="vertical"
          aria-label={t('explorer.resize')}
          aria-valuenow={width}
          aria-valuemin={MIN_TREE}
          aria-valuemax={MAX_TREE}
          tabIndex={0}
          onKeyDown={onResizeKey}
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={() => (drag.current = null)}
          className="w-1 shrink-0 cursor-col-resize bg-transparent hover:bg-primary/30 focus-visible:bg-primary/40 focus-visible:outline-none"
        />
        {selected ? (
          <TableDetail
            key={`${selSchema}.${selTable}`}
            connId={connId}
            schema={selSchema}
            table={selected}
            onNavigate={navigate}
            action={selected.readable ? <MappingAction projectId={project.id} schema={selSchema} table={selected.name} /> : undefined}
          />
        ) : (
          <div className="flex flex-1 items-center justify-center p-8">
            <EmptyState icon={<TableProperties />} title={t('explorer.pickTitle')} body={t('explorer.pickBody')} />
          </div>
        )}
      </div>
    </div>
  )
}

/** "+ Add to project mappings", or "Open mapping →" when the table already has one (OC-035 AC5). */
export function MappingAction({ projectId, schema, table }: { projectId: string; schema: string; table: string }) {
  const { t } = useTranslation()
  const mappings = useMappings(projectId)
  if (!mappings.data) return null
  const existing = mappings.data.find((m) => m.sourceSchema === schema && m.sourceTable === table)
  if (existing) {
    return (
      <Button variant="secondary" size="sm" asChild>
        <Link to={`/projects/${projectId}/mappings/${existing.id}`}>
          {t('explorer.openMapping')}
          <ArrowRight aria-hidden />
        </Link>
      </Button>
    )
  }
  return (
    <Can role="editor">
      <Button size="sm" asChild>
        <Link to={`/projects/${projectId}/tables?select=${encodeURIComponent(`${schema}.${table}`)}`}>
          <Plus aria-hidden />
          {t('explorer.addMapping')}
        </Link>
      </Button>
    </Can>
  )
}
