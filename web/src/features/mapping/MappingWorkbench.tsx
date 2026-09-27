import { useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, Eye, PanelRight, Redo2, ShieldCheck, Undo2 } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useOutletContext, useParams } from 'react-router'
import { toast } from 'sonner'
import type { StoreApi } from 'zustand'
import { AsyncBoundary } from '@/components/AsyncBoundary'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useSourceTable } from '@/features/discovery/api'
import { DocDesigner } from '@/features/docschema/DocDesigner'
import type { Project } from '@/features/projects/api'
import { projectKeys } from '@/features/projects/api'
import { ApiError } from '@/lib/api/client'
import { cn } from '@/lib/utils'
import { mappingKeys, runAutomap, saveMapping, useMapping, validateMapping } from './api'
import { AutomapIntro } from './AutomapIntro'
import { computeStats } from './computeStats'
import { Inspector } from './Inspector'
import { KeyPanel } from './KeyPanel'
import { MappingCanvas } from './MappingCanvas'
import { MappingScoreHeader } from './MappingScoreHeader'
import { MappingTable } from './MappingTable'
import { PreviewDrawer } from './PreviewDrawer'
import { createWorkbenchStore, useWorkbench, WorkbenchContext, type WorkbenchMode, type WorkbenchState } from './store'
import { SuggestionReview } from './SuggestionReview'
import type { MappingView, SourceColumn } from './types'
import { UnmappedPanel } from './UnmappedPanel'
import { useMappingAutosave, type SaveState } from './useMappingAutosave'
import { ValidationPanel } from './ValidationPanel'

const saveTone: Record<SaveState, 'neutral' | 'success' | 'warning' | 'danger'> = { idle: 'neutral', saving: 'neutral', saved: 'success', error: 'danger', conflict: 'danger' }

/** /projects/:projectId/mappings/:mid — loads the mapping and its source table, then opens the workbench. */
export function MappingWorkbenchPage() {
  const project = useOutletContext<Project>()
  const { mid = '' } = useParams()
  const mapping = useMapping(project.id, mid)
  return (
    <AsyncBoundary query={mapping}>
      {(view) => <WorkbenchLoader project={project} view={view} />}
    </AsyncBoundary>
  )
}

function WorkbenchLoader({ project, view }: { project: Project; view: MappingView }) {
  const table = useSourceTable(project.source?.id ?? '', view.sourceSchema, view.sourceTable)
  return <AsyncBoundary query={table}>{(tbl) => <Workbench key={view.id} project={project} view={view} columns={tbl.columns} />}</AsyncBoundary>
}

/** The mapping workbench (OC-018/019): score header, canvas/table/design views, side panels, inspector. */
export function Workbench({ project, view, columns }: { project: Project; view: MappingView; columns: SourceColumn[] }) {
  const [store] = useState<StoreApi<WorkbenchState>>(() => createWorkbenchStore(view, columns))
  return (
    <WorkbenchContext.Provider value={store}>
      <WorkbenchBody project={project} store={store} />
    </WorkbenchContext.Provider>
  )
}

function WorkbenchBody({ project, store }: { project: Project; store: StoreApi<WorkbenchState> }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const view = useWorkbench((s) => s.view)
  const mode = useWorkbench((s) => s.mode)
  const setMode = useWorkbench((s) => s.setMode)
  const setFilter = useWorkbench((s) => s.setFilter)
  const search = useWorkbench((s) => s.search)
  const setSearch = useWorkbench((s) => s.setSearch)
  const conflict = useWorkbench((s) => s.conflict)
  const issues = useWorkbench((s) => s.issues)
  const canUndo = useWorkbench((s) => s.history.past.length > 0)
  const canRedo = useWorkbench((s) => s.history.future.length > 0)
  const undo = useWorkbench((s) => s.undo)
  const redo = useWorkbench((s) => s.redo)
  const replace = useWorkbench((s) => s.replace)
  // Select stable inputs: s.stats() builds a new object per call, which would re-render forever.
  const liveSpec = useWorkbench((s) => s.history.present)
  const columns = useWorkbench((s) => s.columns)
  const embedTables = useWorkbench((s) => s.view.embedTables)
  const live = useMemo(
    () => computeStats(liveSpec, columns.map((c) => c.name), Object.fromEntries(Object.entries(embedTables ?? {}).map(([id, et]) => [id, et.columns.map((c) => c.name)]))),
    [liveSpec, columns, embedTables],
  )
  const autosave = useMappingAutosave(store, project.id)
  // Server-side edits (e.g. adding an embed) first save what's pending.
  const { saveNow } = autosave
  useEffect(() => {
    store.setState({ flush: async () => void (await saveNow()) })
  }, [store, saveNow])
  const [side, setSideTab] = useState('suggestions')
  // Below 1280 px the canvas needs the room, so the panels start closed.
  const [sideOpen, setSideOpen] = useState(() => !window.matchMedia?.('(max-width: 1279px)').matches)
  const setSide = (tab: string) => {
    setSideTab(tab)
    setSideOpen(true)
  }
  const [previewOpen, setPreviewOpen] = useState(false)
  const [rerunning, setRerunning] = useState(false)
  const [validating, setValidating] = useState(false)

  const errors = useMemo(() => issues.filter((i) => i.severity === 'error').length, [issues])
  const warnings = useMemo(() => issues.filter((i) => i.severity === 'warning').length, [issues])

  // Keep the project's mapping list and status in step with saves.
  useEffect(() => {
    if (autosave.state !== 'saved') return
    void qc.invalidateQueries({ queryKey: mappingKeys.list(project.id) })
    void qc.invalidateQueries({ queryKey: projectKeys.one(project.id) })
  }, [autosave.state, view.version, qc, project.id])

  // Undo/redo shortcuts, ignored while typing in a field.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey) || e.key.toLowerCase() !== 'z') return
      const el = e.target as HTMLElement | null
      if (el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.isContentEditable)) return
      e.preventDefault()
      if (e.shiftKey) redo()
      else undo()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [undo, redo])

  const fail = (e: unknown) => toast.error(e instanceof ApiError ? e.message : String(e))

  const rerun = async (m: 'fill' | 'reset') => {
    setRerunning(true)
    try {
      await autosave.saveNow()
      replace(await runAutomap(project.id, view.id, store.getState().version, m))
    } catch (e) {
      fail(e)
    } finally {
      setRerunning(false)
    }
  }

  const validate = async () => {
    setValidating(true)
    try {
      await autosave.saveNow()
      const res = await validateMapping(project.id, view.id)
      store.setState({ issues: res.issues })
      setSide('issues')
      if (res.ready) toast.success(t('validation.ready'))
    } catch (e) {
      fail(e)
    } finally {
      setValidating(false)
    }
  }

  const enableCreate = async () => {
    try {
      await autosave.saveNow()
      const s = store.getState()
      s.saved(await saveMapping(project.id, s.view.id, s.version, s.history.present, { ...s.view.target, createIfMissing: true }))
    } catch (e) {
      fail(e)
    }
  }

  const automap = view.automapStats ?? view.stats

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex flex-wrap items-center gap-3 border-b border-border px-6 py-3">
        <Button variant="ghost" size="icon" asChild>
          <Link to={`/projects/${project.id}/mappings`} aria-label={t('workbench.back')}>
            <ArrowLeft aria-hidden />
          </Link>
        </Button>
        <h1 className="min-w-0 truncate text-[15px] font-semibold">
          {view.sourceSchema}.{view.sourceTable}
          <span className="px-1.5 text-subtle">→</span>
          <span className="font-mono text-[13px]">
            {view.target.bucket}.{view.target.scope}.{view.target.collection}
          </span>
        </h1>
        <Badge tone={saveTone[autosave.state]} aria-live="polite">
          {t(`workbench.save.${autosave.state}`)}
        </Badge>
        {autosave.state === 'error' ? (
          <Button size="sm" variant="link" className="h-6 px-0" onClick={autosave.retry}>
            {t('common.retry')}
          </Button>
        ) : null}
        <div className="ml-auto flex items-center gap-1.5">
          <Button variant="ghost" size="icon" aria-label={t('workbench.side')} aria-pressed={sideOpen} onClick={() => setSideOpen((o) => !o)}>
            <PanelRight aria-hidden />
          </Button>
          <Button variant="ghost" size="icon" aria-label={t('workbench.undo')} disabled={!canUndo} onClick={undo}>
            <Undo2 aria-hidden />
          </Button>
          <Button variant="ghost" size="icon" aria-label={t('workbench.redo')} disabled={!canRedo} onClick={redo}>
            <Redo2 aria-hidden />
          </Button>
          <Button variant="secondary" size="sm" loading={validating} onClick={() => void validate()}>
            <ShieldCheck aria-hidden />
            {t('workbench.validate')}
            {errors ? <Badge tone="danger">{errors}</Badge> : warnings ? <Badge tone="warning">{warnings}</Badge> : null}
          </Button>
          <Button size="sm" onClick={() => setPreviewOpen(true)}>
            <Eye aria-hidden />
            {t('workbench.preview')}
          </Button>
        </div>
      </header>

      {conflict ? (
        <div role="alert" className="flex items-center gap-3 border-b border-danger/30 bg-danger-soft px-6 py-2 text-[13px] text-danger">
          <span className="flex-1">{t('workbench.conflict')}</span>
          <Button size="sm" variant="secondary" onClick={() => void autosave.reload().catch(fail)}>
            {t('workbench.reload')}
          </Button>
          <Button size="sm" variant="destructive" onClick={() => void autosave.overwrite().catch(fail)}>
            {t('workbench.overwrite')}
          </Button>
        </div>
      ) : null}

      <MappingScoreHeader
        automap={automap}
        live={live}
        onFilter={(f) => {
          setFilter(f)
          if (mode === 'design') setMode('canvas')
        }}
        onRerun={(m) => void rerun(m)}
        rerunning={rerunning}
        onRequiredClick={() => {
          setFilter('required')
          setMode('table')
        }}
      />
      <AutomapIntro mappingId={view.id} stats={automap} onReview={() => setSide('suggestions')} />

      <div className="flex items-center gap-3 border-b border-border px-6 py-2">
        <div role="radiogroup" aria-label={t('workbench.view')} className="flex rounded-[var(--radius-control)] border border-border p-0.5">
          {(['canvas', 'table', 'design'] as WorkbenchMode[]).map((m) => (
            <button
              key={m}
              type="button"
              role="radio"
              aria-checked={mode === m}
              onClick={() => setMode(m)}
              className={cn('rounded px-3 py-1 text-xs', mode === m ? 'bg-primary-soft text-primary-soft-foreground' : 'text-muted hover:text-foreground')}
            >
              {t(`workbench.mode.${m}`)}
            </button>
          ))}
        </div>
        {mode !== 'design' ? (
          <input
            type="search"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={t('workbench.search')}
            aria-label={t('workbench.search')}
            className="h-8 w-64 rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 text-[13px]"
          />
        ) : null}
      </div>

      <div className="flex min-h-0 flex-1">
        {/* Not a <main>: the app shell already has the page's one main landmark. */}
        <div className="flex min-w-0 flex-1 flex-col overflow-x-auto">
          {mode === 'canvas' ? <MappingCanvas /> : mode === 'table' ? <MappingTable /> : <DocDesigner projectId={project.id} targetConnId={project.target?.id ?? ''} header={<KeyPanel projectId={project.id} />} />}
        </div>
        <aside aria-label={t('workbench.side')} hidden={!sideOpen} className="flex w-80 shrink-0 flex-col border-l border-border">
          <Tabs value={side} onValueChange={setSideTab} className="flex min-h-0 flex-1 flex-col">
            <TabsList className="px-2">
              <TabsTrigger value="suggestions">{t('workbench.suggestions')}</TabsTrigger>
              <TabsTrigger value="unmapped">{t('workbench.unmapped')}</TabsTrigger>
              <TabsTrigger value="issues">
                {t('workbench.issues')}
                {issues.length ? <span className="ml-1 text-subtle">{issues.length}</span> : null}
              </TabsTrigger>
            </TabsList>
            <TabsContent value="suggestions" className="min-h-0 flex-1 overflow-y-auto">
              <SuggestionReview onMapRemaining={() => setSide('unmapped')} />
            </TabsContent>
            <TabsContent value="unmapped" className="min-h-0 flex-1 overflow-y-auto">
              <UnmappedPanel />
            </TabsContent>
            <TabsContent value="issues" className="min-h-0 flex-1 overflow-y-auto">
              <ValidationPanel onEnableCreate={() => void enableCreate()} />
            </TabsContent>
          </Tabs>
        </aside>
      </div>
      <Inspector projectId={project.id} />
      <PreviewDrawer projectId={project.id} open={previewOpen} onOpenChange={setPreviewOpen} />
    </div>
  )
}
