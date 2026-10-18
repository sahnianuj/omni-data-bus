import { useEffect, useId, useMemo, useRef, useState } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { ChevronDown, Download } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useSchema, useSourceTable } from '@/features/discovery/api'
import { generateFromTable } from '@/features/docschema/generate'
import { mappingSheet, runAutomapScope } from '@/features/mapping/api'
import { useTablesHops } from '@/features/mapping/relationsApi'
import { fieldTypeOf } from '@/features/docschema/generate'
import { boundRelations } from './scope'
import { computeStats } from '@/features/mapping/computeStats'
import { connect, OpError, replaceSource, activeFor } from '@/features/mapping/mappingOps'
import { srcKey } from '@/features/mapping/embeds'
import { useWorkbench, useWorkbenchStore } from '@/features/mapping/store'
import type { CanvasColumn, JunctionCandidate, MappingSpec } from '@/features/mapping/types'
import { ApiError } from '@/lib/api/client'
import { BindDialog } from './BindDialog'
import { Can } from '@/features/auth/Can'
import { ReviewBanner, useReview } from './ReviewBanner'
import { ProposalPanel } from './ProposalPanel'
import { pendingImportKey } from '@/features/mapping/NewFromDesign'
import type { ReviewFilter } from './review'
import { proposeMapping } from '@/features/mapping/api'
import { useSearchParams } from 'react-router'
import { Sparkles } from 'lucide-react'
import { ComposerInspector, useScopeColumns } from './ComposerInspector'
import { foreignKeyOf, scopeDepth } from './bindPrefill'
import { addNode, bindGroupRows, blank, collapseToId, nodeOf, setDrivingTable, setGroupBy, singularName, unboundContainers, type ForeignKey, type TableRef } from './composerOps'
import { rename as renameStyle } from '@/lib/naming'
import type { AvailableHop, Instance } from './documentSources'
import { CombineRowsDialog } from './CombineRowsDialog'
import { EmbedKeyDialog } from './EmbedKeyDialog'
import { DocumentTree } from './DocumentTree'
import { GroupPicker } from './GroupPicker'
import { ImportDialog } from './ImportDialog'
import { ProblemsPanel } from './ProblemsPanel'
import { SamplePane } from './SamplePane'
import { ScoreHeader } from './ScoreHeader'
import { SourcesPane } from './SourcesPane'
import { SuggestionsPanel } from './SuggestionsPanel'
import { HelpLink } from '@/components/HelpLink'

type DialogState =
  | { kind: 'bind'; nodeId: string; table: TableRef; lookup: boolean; mark?: number }
  | { kind: 'pick'; nodeId: string; lookup: boolean }
  | { kind: 'import'; nodeId: string | null; initial?: { text: string; fileName?: string }; propose?: boolean }
  | { kind: 'start'; start: 'blank' | 'generated' }
  | { kind: 'suggest' }
  | { kind: 'embed'; fieldId: string; fk: ForeignKey }
  | { kind: 'combine'; columns: string[]; relationId?: string }
  | null

const key = (t: TableRef) => `${t.schema}.${t.name}`

/** Below 1024 px the three panes collapse into tabs (OC-048). */
function useNarrow() {
  const [narrow, setNarrow] = useState(() => !!window.matchMedia?.('(max-width: 1023px)').matches)
  useEffect(() => {
    const m = window.matchMedia?.('(max-width: 1023px)')
    if (!m) return
    const on = () => setNarrow(m.matches)
    m.addEventListener?.('change', on)
    return () => m.removeEventListener?.('change', on)
  }, [])
  return narrow
}

/**
 * The import a "New mapping from a design" of an already mapped table opens with (OC-170): the
 * design it stashed for this mapping, to update it (the import's Update keeps what's mapped).
 */
function pendingImport(pending: boolean, mappingId: string): DialogState {
  if (!pending) return null
  let initial: { text: string; fileName?: string } | undefined
  try {
    const raw = window.sessionStorage.getItem(pendingImportKey(mappingId))
    window.sessionStorage.removeItem(pendingImportKey(mappingId))
    if (raw) initial = JSON.parse(raw) as { text: string; fileName?: string }
  } catch {
    // No session storage: an empty import.
  }
  return { kind: 'import', nodeId: null, initial, propose: true }
}

/**
 * The document composer (OC-048): design the target JSON first, then bind its objects and arrays
 * to source tables and map its fields. Sources (relationship tree) · Document (tree) · Inspector,
 * with the score, problems and a live sample. It edits the workbench's spec, so undo, autosave
 * and conflicts work as on the canvas.
 */
export function Composer() {
  const { t } = useTranslation()
  const store = useWorkbenchStore()
  const view = useWorkbench((s) => s.view)
  const spec = useWorkbench((s) => s.history.present)
  const columns = useWorkbench((s) => s.columns)
  const apply = useWorkbench((s) => s.apply)
  const replace = useWorkbench((s) => s.replace)
  const select = useWorkbench((s) => s.select)
  const selection = useWorkbench((s) => s.selection)
  const [params, setParams] = useSearchParams()
  const [dialog, setDialog] = useState<DialogState>(() => pendingImport(params.get('import') === 'pending', view.id))
  const review = useWorkbench((s) => s.review)
  const reviewer = useReview()
  const [reviewFilter, setReviewFilter] = useState<ReviewFilter>('all')
  const [proposing, setProposing] = useState(false)
  const [focusSource, setFocusSource] = useState(0)
  const [busy, setBusy] = useState(false)
  const [tab, setTab] = useState('document')
  const narrow = useNarrow()
  const conn = `${view.projectId}::source`
  const own = useMemo(() => ({ schema: view.sourceSchema, name: view.sourceTable }), [view.sourceSchema, view.sourceTable])
  const driving = spec.root?.table ?? own
  const snap = useSchema(conn)
  const drivingTable = useSourceTable(conn, driving.schema, driving.name)
  const drivingId = useId()
  const embedded = useMemo(() => Object.fromEntries(Object.entries(view.embedTables ?? {}).map(([id, et]) => [id, et.columns.map((c) => c.name)])), [view.embedTables])
  const live = useMemo(() => computeStats(spec, columns.map((c) => c.name), embedded), [spec, columns, embedded])
  const byNode = live.byNode ?? [{ nodeId: spec.docSchema.root.id, path: 'root', mapped: live.mappedTargetLeaves, total: live.totalTargetLeaves }]
  const selectedId = selection?.kind === 'field' ? selection.id : spec.docSchema.root.id
  const unbound = useMemo(() => unboundContainers(spec), [spec])
  // After an import, say what's left to bind (OC-060) until it's dismissed or all bound.
  const [bannerOff, setBannerOff] = useState(false)
  const showBanner = !!spec.docSchema.imported && unbound.arrays + unbound.objects > 0 && !bannerOff
  const parts = [unbound.arrays ? t('composer.suggest.arrays', { count: unbound.arrays }) : '', unbound.objects ? t('composer.suggest.objects', { count: unbound.objects }) : ''].filter(Boolean)
  const bannerList = parts.length === 2 ? t('composer.suggest.and', { a: parts[0], b: parts[1] }) : (parts[0] ?? '')
  const dropColumnsFor = useScopeColumns(spec, selectedId)

  // The driving table's columns are the root's (they change with the driving table, OC-047).
  useEffect(() => {
    const cols = drivingTable.data?.columns
    if (cols && store.getState().columns !== cols && (store.getState().columns.length !== cols.length || store.getState().columns.some((c, i) => c.name !== cols[i]!.name))) {
      store.setState({ columns: cols })
    }
  }, [drivingTable.data, store])

  const fail = (e: unknown) => toast.error(e instanceof ApiError || e instanceof OpError ? e.message : String(e))
  // Design-first (OC-163): the whole mapping proposed from the connected source, then reviewed
  // (OC-168). base keeps what's already bound and mapped (OC-170).
  const proposeNow = async (base: boolean) => {
    setProposing(true)
    try {
      const s = store.getState()
      const p = await proposeMapping(s.view.projectId, s.view.id, { specOverride: s.history.present, base })
      reviewer.start(p)
      setReviewFilter('all')
    } catch (e) {
      fail(e)
    } finally {
      setProposing(false)
    }
  }
  // Opened from "New mapping from a design" (OC-163, OC-170): ?propose=1 proposes once the
  // composer is up; ?import=pending opened the import (the dialog's first state, below).
  const proposeOnOpen = useRef(params.get('propose') === '1' ? { base: params.get('base') === '1' } : null)
  useEffect(() => {
    if (!params.has('propose') && !params.has('import')) return
    const next = new URLSearchParams(params)
    next.delete('propose')
    next.delete('base')
    next.delete('import')
    setParams(next, { replace: true })
    const once = proposeOnOpen.current
    proposeOnOpen.current = null
    if (once && spec.docSchema.root.children?.length) queueMicrotask(() => void proposeNow(once.base))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  const run = (fn: (s: MappingSpec) => MappingSpec) => {
    try {
      apply(fn)
    } catch (e) {
      if (e instanceof OpError) toast.error(e.message)
      else throw e
    }
  }

  const autoMap = async (body: { nodeId: string } | { allScopes: true }) => {
    setBusy(true)
    try {
      await store.getState().flush()
      const s = store.getState()
      replace(await runAutomapScope(view.projectId, s.view.id, s.version, body))
    } catch (e) {
      fail(e)
    } finally {
      setBusy(false)
    }
  }

  const start = (kind: 'blank' | 'generated') => {
    if (kind === 'blank') run((s) => blank(s))
    else {
      const generated = generateFromTable(columns, { naming: 'camelCase', nestByPrefix: false })
      run((s) => ({ ...blank(s), docSchema: { ...generated, root: { ...generated.root, id: s.docSchema.root.id } } }))
      void autoMap({ nodeId: spec.docSchema.root.id })
    }
    select({ kind: 'field', id: spec.docSchema.root.id })
  }

  const dropColumn = (fieldId: string, c: { relation: string; column: string; from?: 'via' }) => {
    const found = dropColumnsFor.flatMap((g) => g.columns).find((x) => x.entry.relationId === c.relation && x.column.name === c.column && (x.from ?? '') === (c.from ?? ''))
    const column: CanvasColumn | undefined = found
      ? c.relation
        ? { ...found.column, name: srcKey({ column: c.column, relation: c.relation, from: c.from }), embed: c.relation, column: c.column, ...(c.from ? { from: 'via' as const } : {}) }
        : found.column
      : undefined
    if (!column) return toast.error(t('composer.outOfScope'))
    run((s) => {
      const existing = activeFor(s, fieldId)
      if (existing) return replaceSource(s, existing.id, column)
      const res = connect(s, column, fieldId)
      return res.outcome === 'connected' ? res.spec : s
    })
  }

  // The mapping sheet (OC-061): saved first, so it describes what's on screen.
  const downloadSheet = async (format: 'csv' | 'md') => {
    try {
      await store.getState().flush()
      const { text, fileName } = await mappingSheet(view.projectId, store.getState().view.id, format)
      const url = URL.createObjectURL(new Blob([text], { type: format === 'csv' ? 'text/csv' : 'text/markdown' }))
      const a = window.document.createElement('a') // `document` is the pane below
      a.href = url
      a.download = fileName
      a.click()
      URL.revokeObjectURL(url)
      toast.success(t('composer.sheet.done', { name: fileName }))
    } catch (e) {
      fail(e)
    }
  }

  // One document per group (OC-063): the group's rows are ordered by the rest of the key.
  const primaryKey = drivingTable.data?.primaryKey ?? []
  const groupBy = spec.root?.groupBy ?? []
  const groupRows = (nodeId: string) => {
    const orderBy = primaryKey.filter((k) => !groupBy.includes(k)).map((column) => ({ column }))
    run((s) => bindGroupRows(s, nodeId, orderBy).spec)
  }
  const changeGroup = (cols: string[]) => {
    run((s) => setGroupBy(s, cols, own))
    toast.success(cols.length ? t('composer.group.done', { columns: cols.join(', ') }) : t('composer.group.off'))
  }

  // Foreign keys in the document (OC-064): embed the row a key refers to, or collapse it back.
  // Each scope table's own hops (OC-141) find keys at any depth (OC-143).
  const { hopsOf, maxRelationDepth } = useTablesHops(conn, [key(driving), ...spec.relations.map((r) => key(r.table))])
  const fkOf = (fieldId: string) => foreignKeyOf(hopsOf, spec, fieldId, own)
  // At the depth limit the action shows, disabled, with why (OC-142 AC3, OC-143).
  const embedBlocked = (fieldId: string) => {
    const depth = scopeDepth(spec, fieldId)
    return maxRelationDepth != null && depth >= maxRelationDepth ? t('embed.depthLimit', { depth, max: maxRelationDepth }) : undefined
  }
  const embed = (fieldId: string) => {
    const fk = fkOf(fieldId)
    if (fk && !embedBlocked(fieldId)) setDialog({ kind: 'embed', fieldId, fk })
  }
  const collapsible = (nodeId: string) => {
    const r = boundRelations(spec).get(nodeId)
    return !!r && !r.group && r.kind === 'many-to-one' && r.join?.length === 1 && nodeOf(spec, nodeId)?.type === 'object'
  }
  const collapse = (nodeId: string) => {
    const r = boundRelations(spec).get(nodeId)
    const scopeCols = r?.parentId ? spec.relations.find((x) => x.id === r.parentId)?.table : driving
    const col = scopeCols && key(scopeCols) === key(driving) ? columns.find((c) => c.name === r?.join?.[0]?.parent) : undefined
    run((s) => collapseToId(s, nodeId, col ? { type: fieldTypeOf(col).type } : {}))
  }

  const tables = useMemo(
    () => (snap.data?.schemas ?? []).flatMap((s) => s.tables.filter((tb) => tb.readable).map((tb) => ({ schema: s.name, name: tb.name }))),
    [snap.data],
  )

  // "In this document" (OC-142): a node for the hop under the instance's object, then the Bind
  // dialog pre-filled with the hop's join; the node, the bind and the auto-map are one undo step
  // (OC-142 AC2), and cancelling removes the node.
  const embedHop = (inst: Instance, hop: AvailableHop | JunctionCandidate) => {
    const table = 'from' in hop ? hop.to : hop.table
    const isObject = 'from' in hop && hop.kind === 'many-to-one'
    const mark = store.getState().history.past.length
    let id = ''
    run((s) => {
      const r = addNode(s, inst.containerId, isObject ? 'object' : 'array', isObject ? singularName(table.name) : renameStyle(table.name))
      id = r.id
      return r.spec
    })
    if (id) setDialog({ kind: 'bind', nodeId: id, table, lookup: false, mark })
  }
  const lookupHop = (inst: Instance, hop: AvailableHop) => setDialog({ kind: 'bind', nodeId: inst.containerId, table: hop.to, lookup: true })
  const sources = <SourcesPane onBind={(nodeId, table, lookup) => setDialog({ kind: 'bind', nodeId, table, lookup })} onGroupRows={groupRows} onEmbedHop={embedHop} onLookupHop={lookupHop} />
  // A .json file dropped on the document opens the import with it (OC-059).
  const dropFile = async (nodeId: string | null, file: File) => setDialog({ kind: 'import', nodeId, initial: { text: await file.text(), fileName: file.name } })
  const document = (
    <div
      className="flex min-h-0 flex-1 flex-col"
      onDragOver={(e) => e.dataTransfer.types.includes('Files') && e.preventDefault()}
      onDrop={(e) => {
        const file = e.dataTransfer.files?.[0]
        if (!file) return
        e.preventDefault()
        void dropFile(null, file)
      }}
    >
      <div className="min-h-24 flex-1 overflow-y-auto">
        <DocumentTree
          reviewFilter={review ? reviewFilter : 'all'}
          onBindTable={(nodeId, lookup) => setDialog({ kind: 'pick', nodeId, lookup: !!lookup })}
          onDropTable={(nodeId, table, lookup) => setDialog({ kind: 'bind', nodeId, table, lookup })}
          onDropColumn={dropColumn}
          onMapColumn={(id) => {
            select({ kind: 'field', id })
            if (narrow) setTab('inspector')
            setFocusSource((n) => n + 1)
          }}
          onPaste={(nodeId) => setDialog({ kind: 'import', nodeId })}
          onDropFile={(nodeId, file) => void dropFile(nodeId, file)}
          onGroupRows={groupRows}
          embeddable={(id) => fkOf(id)?.table.name}
          embedBlocked={embedBlocked}
          onEmbed={embed}
          collapsible={collapsible}
          onCollapse={collapse}
        />
      </div>
      <div className="max-h-28 min-h-0 overflow-y-auto border-t border-border">
        <ProblemsPanel onCombineRows={(columns, relationId) => setDialog({ kind: 'combine', columns, relationId })} />
      </div>
    </div>
  )
  const inspector = (
    <>
      <ProposalPanel fieldId={selectedId} onUseColumn={dropColumn} onImportItems={(nodeId) => setDialog({ kind: 'import', nodeId })} />
      <ComposerInspector focusSource={focusSource} onBindTable={(nodeId, lookup) => setDialog({ kind: 'pick', nodeId, lookup: !!lookup })} embedTable={fkOf(selectedId)?.table.name} embedBlocked={embedBlocked(selectedId)} onEmbed={embed} />
    </>
  )

  return (
    <div className="flex min-h-0 flex-1 flex-col" data-testid="composer">
      <div className="flex flex-wrap items-center gap-1.5 border-b border-border px-4 py-1.5 text-[13px]">
        <Button size="sm" variant="secondary" onClick={() => setDialog({ kind: 'import', nodeId: null })}>
          {t('composer.start.paste')}
        </Button>
        {spec.docSchema.root.children?.length && !review ? (
          <Can role="editor">
            <Button size="sm" variant="ghost" disabled={proposing} onClick={() => void proposeNow(spec.relations.length > 0 || spec.fieldMappings.some((m) => m.state === 'active'))}>
              <Sparkles aria-hidden />
              {t('propose.propose')}
            </Button>
          </Can>
        ) : null}
        <DropdownMenu.Root>
          <DropdownMenu.Trigger asChild>
            <Button size="sm" variant="ghost">
              {t('composer.start.label')}
              <ChevronDown aria-hidden />
            </Button>
          </DropdownMenu.Trigger>
          <DropdownMenu.Portal>
            <DropdownMenu.Content align="start" className="z-50 min-w-48 rounded-md border border-border bg-surface p-1 shadow-lg">
              {(['blank', 'generated'] as const).map((k) => (
                <DropdownMenu.Item key={k} onSelect={() => setDialog({ kind: 'start', start: k })} className="rounded px-2 py-1.5 text-[13px] outline-none hover:bg-surface-muted focus:bg-surface-muted">
                  {t(`composer.start.${k}`)}
                </DropdownMenu.Item>
              ))}
            </DropdownMenu.Content>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
        {spec.docSchema.imported ? (
          <label className="flex items-center gap-1.5 text-xs text-muted" title={`${t('composer.strictLong')} ${t('composer.strictHint')}`}>
            <input type="checkbox" checked={!!spec.referenceStrict} onChange={(e) => run((s) => ({ ...s, referenceStrict: e.target.checked }))} />
            {t('composer.strict')}
          </label>
        ) : null}
        <DropdownMenu.Root>
          <DropdownMenu.Trigger asChild>
            <Button size="sm" variant="ghost">
              <Download aria-hidden />
              {t('composer.sheet.label')}
              <ChevronDown aria-hidden />
            </Button>
          </DropdownMenu.Trigger>
          <DropdownMenu.Portal>
            <DropdownMenu.Content align="start" className="z-50 min-w-48 rounded-md border border-border bg-surface p-1 shadow-lg">
              {(['csv', 'md'] as const).map((f) => (
                <DropdownMenu.Item key={f} onSelect={() => void downloadSheet(f)} className="rounded px-2 py-1.5 text-[13px] outline-none hover:bg-surface-muted focus:bg-surface-muted">
                  {t(`composer.sheet.${f}`)}
                </DropdownMenu.Item>
              ))}
            </DropdownMenu.Content>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
        <span className="ml-auto" />
        <GroupPicker table={driving.name} columns={columns.map((c) => c.name)} primaryKey={primaryKey} groupBy={groupBy} onChange={changeGroup} />
        <label htmlFor={drivingId} className="flex items-center gap-2 text-xs font-medium text-muted">
          <span className="sr-only">{t('composer.driving')}</span>
          <select
            id={drivingId}
            title={t('composer.driving')}
            value={key(driving)}
            onChange={(e) => {
              const tb = tables.find((x) => key(x) === e.target.value)
              if (tb) run((s) => setDrivingTable(s, tb))
            }}
            className="h-8 rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 text-[13px] text-foreground"
          >
            {[driving, ...tables.filter((x) => key(x) !== key(driving))].map((x) => (
              <option key={key(x)} value={key(x)}>
                {key(x)}
              </option>
            ))}
          </select>
        </label>
      </div>
      <ScoreHeader
        byNode={byNode}
        busy={busy}
        onAutoMapAll={() => void autoMap({ allScopes: true })}
        onSelect={(id) => select({ kind: 'field', id })}
        unbound={unbound.arrays + unbound.objects}
        onSuggest={() => setDialog({ kind: 'suggest' })}
      />
      <ReviewBanner filter={reviewFilter} onFilter={setReviewFilter} />
      {showBanner && !review ? (
        <div role="status" className="flex flex-wrap items-center gap-2 border-b border-border bg-primary-soft px-4 py-2 text-[13px] text-primary-soft-foreground">
          <span>{t('composer.suggest.banner', { list: bannerList })}</span>
          <Button size="sm" onClick={() => setDialog({ kind: 'suggest' })}>
            {t('composer.suggest.open')}
          </Button>
          <Button size="sm" variant="ghost" className="ml-auto" onClick={() => setBannerOff(true)}>
            {t('composer.suggest.dismiss')}
          </Button>
        </div>
      ) : null}
      {narrow ? (
        <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col">
          <TabsList className="px-2">
            <TabsTrigger value="sources">{t('composer.sources.title')}</TabsTrigger>
            <TabsTrigger value="document">{t('composer.document')}</TabsTrigger>
            <TabsTrigger value="inspector">{t('composer.inspector')}</TabsTrigger>
          </TabsList>
          <TabsContent value="sources" className="flex min-h-0 flex-1 flex-col">
            {sources}
          </TabsContent>
          <TabsContent value="document" className="flex min-h-0 flex-1 flex-col">
            {document}
          </TabsContent>
          <TabsContent value="inspector" className="min-h-0 flex-1 overflow-y-auto">
            {inspector}
          </TabsContent>
        </Tabs>
      ) : (
        <div className="grid min-h-0 flex-1 grid-cols-[260px_minmax(0,1fr)_340px]">
          <div className="flex min-h-0 flex-col border-r border-border">{sources}</div>
          <div className="flex min-h-0 flex-col overflow-hidden">
            <div className="flex items-center gap-1.5 px-3 pt-3">
              <h2 className="text-xs font-semibold uppercase tracking-wide text-muted">{t('composer.document')}</h2>
              <HelpLink topic="composer" />
            </div>
            {document}
          </div>
          <div className="flex min-h-0 flex-col overflow-y-auto border-l border-border">{inspector}</div>
        </div>
      )}
      <SamplePane />

      {dialog?.kind === 'bind' ? (
        <BindDialog
          nodeId={dialog.nodeId}
          table={dialog.table}
          lookup={dialog.lookup}
          onClose={() => {
            if (dialog.mark != null) store.getState().squash(dialog.mark)
            setDialog(null)
          }}
          onCancel={() => {
            if (dialog.mark != null) store.getState().revert(dialog.mark)
            setDialog(null)
          }}
        />
      ) : null}
      {dialog?.kind === 'pick' ? (
        <TablePicker
          tables={tables}
          node={nodeOf(spec, dialog.nodeId)?.name ?? '$'}
          onPick={(table) => setDialog({ kind: 'bind', nodeId: dialog.nodeId, table, lookup: dialog.lookup })}
          onClose={() => setDialog(null)}
        />
      ) : null}
      {dialog?.kind === 'combine' ? <CombineRowsDialog columns={dialog.columns} relationId={dialog.relationId} onClose={() => setDialog(null)} onDone={() => setDialog(null)} /> : null}
      {dialog?.kind === 'embed' ? (
        <EmbedKeyDialog
          fieldId={dialog.fieldId}
          fk={dialog.fk}
          onClose={() => setDialog(null)}
          onDone={(id) => {
            setDialog(null)
            select({ kind: 'field', id })
            void autoMap({ nodeId: id })
          }}
        />
      ) : null}
      {dialog?.kind === 'suggest' ? (
        <SuggestionsPanel
          onClose={() => setDialog(null)}
          onAutoMapAll={() => void autoMap({ allScopes: true })}
          onOther={(nodeId, table) => setDialog(table ? { kind: 'bind', nodeId, table, lookup: false } : { kind: 'pick', nodeId, lookup: false })}
        />
      ) : null}
      {dialog?.kind === 'import' ? <ImportDialog nodeId={dialog.nodeId} initial={dialog.initial} proposeByDefault={dialog.propose} onClose={() => setDialog(null)} onPropose={(base) => void proposeNow(base)} /> : null}
      <ConfirmDialog
        open={dialog?.kind === 'start'}
        onOpenChange={(o) => !o && setDialog(null)}
        title={t('composer.start.confirmTitle')}
        description={t('composer.start.confirmBody')}
        confirmLabel={t('composer.start.confirm')}
        onConfirm={() => {
          if (dialog?.kind === 'start') start(dialog.start)
          setDialog(null)
        }}
      />
    </div>
  )
}

/** "Bind to table…": the keyboard equivalent of dropping a table on a node. */
function TablePicker({ tables, node, onPick, onClose }: { tables: TableRef[]; node: string; onPick: (t: TableRef) => void; onClose: () => void }) {
  const { t } = useTranslation()
  const [q, setQ] = useState('')
  const shown = tables.filter((x) => key(x).toLowerCase().includes(q.trim().toLowerCase()))
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('composer.pick.title', { node })}</DialogTitle>
          <DialogDescription>{t('composer.pick.body')}</DialogDescription>
        </DialogHeader>
        <Input autoFocus aria-label={t('composer.pick.filter')} placeholder={t('composer.pick.filter')} value={q} onChange={(e) => setQ(e.target.value)} />
        <ul aria-label={t('composer.pick.tables')} className="flex max-h-72 flex-col overflow-y-auto">
          {shown.map((x) => (
            <li key={key(x)}>
              <button type="button" onClick={() => onPick(x)} className="w-full rounded px-2 py-1.5 text-left text-[13px] hover:bg-surface-muted">
                {key(x)}
              </button>
            </li>
          ))}
        </ul>
      </DialogContent>
    </Dialog>
  )
}
