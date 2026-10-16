import { useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, Eye, PanelRight, PanelTopClose, PanelTopOpen, Redo2, ShieldCheck, Undo2 } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useLocation, useOutletContext, useParams } from 'react-router'
import { toast } from 'sonner'
import type { StoreApi } from 'zustand'
import { AsyncBoundary } from '@/components/AsyncBoundary'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ResizeHandle, usePaneWidth } from '@/components/ui/resize-handle'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useSourceTable } from '@/features/discovery/api'
import { DocDesigner } from '@/features/docschema/DocDesigner'
import type { Project } from '@/features/projects/api'
import { projectKeys } from '@/features/projects/api'
import { ApiError } from '@/lib/api/client'
import { maximizeStore, useMaximizable, useMaximized } from '@/lib/maximize'
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
import { ViewBar } from './ViewBar'
import { BAR_VIEWS, composerHintDismissed, dismissComposerHint, initialView, rememberView } from './viewPrefs'
import { formatLocation, useKindCapabilities } from '@/features/targets/kinds'
import { Composer } from '@/features/composer/Composer'
import { toCanvas, toComposed } from '@/features/composer/modeSwitch'
import { CombineRowsDialog } from '@/features/composer/CombineRowsDialog'

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
  // A composed mapping's root columns are its driving table's (OC-047).
  const drv = view.spec.mode === 'composed' && view.spec.root ? view.spec.root.table : { schema: view.sourceSchema, name: view.sourceTable }
  const table = useSourceTable(project.source?.id ?? '', drv.schema, drv.name)
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
  const caps = useKindCapabilities(project.target?.type)
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
  // The cached mapping never refetches by itself (staleTime: Infinity), so it follows every
  // save and server-side change (an embed, auto-map): coming back reopens what was saved.
  const cache = useCallback((v: MappingView) => qc.setQueryData(mappingKeys.one(project.id, v.id), v), [qc, project.id])
  useEffect(() => store.subscribe((s, prev) => void (s.view !== prev.view && cache(s.view))), [store, cache])
  const autosave = useMappingAutosave(store, project.id, 800, cache)
  // Server-side edits (e.g. adding an embed) first save what's pending.
  const { saveNow } = autosave
  useEffect(() => {
    store.setState({ flush: async () => void (await saveNow()) })
  }, [store, saveNow])
  // A "Fix" link (OC-083) opens the Issues tab; otherwise suggestions first.
  const issuesLink = new URLSearchParams(useLocation().search).get('panel') === 'issues'
  const [side, setSideTab] = useState(issuesLink ? 'issues' : 'suggestions')
  // Below 1280 px the canvas needs the room, so the panels start closed (unless a link asks).
  const sidePane = usePaneWidth('mapping.side', 320, 260, 640)
  const [sideOpen, setSideOpen] = useState(() => issuesLink || !window.matchMedia?.('(max-width: 1279px)').matches)
  const setSide = (tab: string) => {
    setSideTab(tab)
    setSideOpen(true)
  }
  const [previewOpen, setPreviewOpen] = useState(false)
  // Maximized, the page chrome and score strip step aside so the design gets the height.
  useMaximizable()
  const maximized = useMaximized()
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
  const apply = useWorkbench((s) => s.apply)
  // Canvas and Composer edit the same relation tree; switching sets the spec's mode (OC-048 AC6).
  const switchMode = (m: WorkbenchMode) => {
    const spec = store.getState().history.present
    if (m === 'composer' && spec.mode !== 'composed') apply((s) => toComposed(s, { schema: view.sourceSchema, name: view.sourceTable }))
    if (m === 'canvas' && spec.mode === 'composed') {
      const res = toCanvas(spec, { schema: view.sourceSchema, name: view.sourceTable })
      if ('blocked' in res) {
        toast.error(t('composer.canvasBlocked', { table: `${res.blocked.schema}.${res.blocked.name}`, own: `${view.sourceSchema}.${view.sourceTable}` }))
        return
      }
      apply(() => res.spec)
    }
    setMode(m)
    rememberView(view.id, m)
  }

  // Open in the linked, composed or remembered view (OC-062), once.
  const location = useLocation()
  const opened = useRef(false)
  useEffect(() => {
    if (opened.current) return
    opened.current = true
    const want = initialView(view.id, view.spec.mode === 'composed', location.search)
    if (want !== store.getState().mode) switchMode(want)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only on open
  }, [])

  // Alt+1…3 switch views from anywhere in the workbench.
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (!e.altKey || e.ctrlKey || e.metaKey) return
      const i = ['Digit1', 'Digit2', 'Digit3'].indexOf(e.code)
      if (i < 0) return
      e.preventDefault()
      switchMode(BAR_VIEWS[i]!)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  })

  // "Combine rows with the same key" from the Canvas opens the result in the Composer (OC-066).
  const [combining, setCombining] = useState<string[] | null>(null)

  // The Composer is made for nested documents: say so once on the Canvas (OC-062).
  const [hintOff, setHintOff] = useState(composerHintDismissed)
  const nested = liveSpec.relations.some((r) => !r.lookupOnly && r.targetFieldId) || (!!liveSpec.docSchema.imported && JSON.stringify(liveSpec.docSchema.root).includes('"type":"array"'))
  const showComposerHint = mode === 'canvas' && nested && !hintOff
  const drivingName = liveSpec.mode === 'composed' && liveSpec.root ? `${liveSpec.root.table.schema}.${liveSpec.root.table.name}` : `${view.sourceSchema}.${view.sourceTable}`

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex flex-wrap items-center gap-3 border-b border-border px-6 py-3">
        <Button variant="ghost" size="icon" asChild>
          <Link to={`/projects/${project.id}/mappings`} aria-label={t('workbench.back')}>
            <ArrowLeft aria-hidden />
          </Link>
        </Button>
        <h1 className="min-w-0 truncate text-[15px] font-semibold">
          {drivingName}
          <span className="px-1.5 text-subtle">→</span>
          <span className="font-mono text-[13px]">
            {formatLocation(view.location)}
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

      <ViewBar
        mode={mode}
        onChange={switchMode}
        trailing={
          <Button
            variant="ghost"
            size="icon"
            aria-label={t(maximized ? 'workbench.restoreHeight' : 'workbench.maximizeHeight')}
            title={t(maximized ? 'workbench.restoreHeight' : 'workbench.maximizeHeight')}
            aria-pressed={maximized}
            onClick={maximizeStore.getState().toggle}
          >
            {maximized ? <PanelTopOpen aria-hidden /> : <PanelTopClose aria-hidden />}
          </Button>
        }
      />
      {mode === 'composer' || maximized ? null : (
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
      )}
      {mode === 'composer' || maximized ? null : <AutomapIntro mappingId={view.id} stats={automap} onReview={() => setSide('suggestions')} />}

      {mode !== 'design' && mode !== 'composer' ? (
        <div className="flex items-center gap-3 border-b border-border px-6 py-2">
          {mode === 'table' ? (
            <Button size="sm" variant="secondary" onClick={() => switchMode('canvas')}>
              <ArrowLeft aria-hidden />
              {t('workbench.backToCanvas')}
            </Button>
          ) : null}
          <input
            type="search"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={t('workbench.search')}
            aria-label={t('workbench.search')}
            className="h-8 w-64 rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 text-[13px]"
          />
        </div>
      ) : null}
      {showComposerHint ? (
        <div role="status" className="flex flex-wrap items-center gap-2 border-b border-border bg-primary-soft px-6 py-2 text-[13px] text-primary-soft-foreground">
          <span>{t('workbench.composerHint')}</span>
          <Button size="sm" onClick={() => switchMode('composer')}>
            {t('workbench.openComposer')}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            className="ml-auto"
            onClick={() => {
              dismissComposerHint()
              setHintOff(true)
            }}
          >
            {t('workbench.dontShowAgain')}
          </Button>
        </div>
      ) : null}

      <div className="flex min-h-0 flex-1">
        {/* Not a <main>: the app shell already has the page's one main landmark. */}
        <div className="flex min-w-0 flex-1 flex-col overflow-x-auto">
          {mode === 'composer' ? <Composer /> : mode === 'canvas' ? <MappingCanvas /> : mode === 'table' ? <MappingTable /> : <DocDesigner projectId={project.id} targetConnId={project.target?.id ?? ''} header={<KeyPanel projectId={project.id} supportsExpiry={caps.supportsExpiry} />} />}
        </div>
        <aside aria-label={t('workbench.side')} hidden={!sideOpen || mode === 'composer'} className="relative flex shrink-0 flex-col border-l border-border" style={{ width: sidePane.width }}>
          <ResizeHandle pane={sidePane} edge="left" label={t('workbench.resizeSide')} />
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
              <ValidationPanel onEnableCreate={() => void enableCreate()} onCombineRows={setCombining} />
            </TabsContent>
          </Tabs>
        </aside>
      </div>
      {mode === 'composer' ? null : <Inspector projectId={project.id} />}
      {combining ? (
        <CombineRowsDialog
          columns={combining}
          onClose={() => setCombining(null)}
          onDone={() => {
            setCombining(null)
            switchMode('composer')
          }}
        />
      ) : null}
      <PreviewDrawer projectId={project.id} open={previewOpen} onOpenChange={setPreviewOpen} />
    </div>
  )
}
