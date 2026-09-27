import { useVirtualizer } from '@tanstack/react-virtual'
import { ChevronRight, CornerDownRight, EyeOff, KeyRound, Plus, X } from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type CSSProperties, type DragEvent, type KeyboardEvent, type UIEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Can } from '@/features/auth/Can'
import { cn } from '@/lib/utils'
import { buildEdges, parentMap, type Edge } from './buildEdges'
import { canConnect } from './canConnect'
import { canvasLeaves, embedColumns, embedContainer, fieldsById, removeEmbed } from './embeds'
import { addSource, connect, createFieldAndMap, disconnect, OpError, replaceSource } from './mappingOps'
import { RelatedTables } from './RelatedTables'
import { selectVisible, type SourceState, type TargetState } from './selectVisible'
import { useWorkbench } from './store'
import type { CanvasColumn, DocField, Embed, MappingSpec } from './types'

export const ROW = 32
const HEADER = 36
const GUTTER = 160

type TargetRow = { field: DocField; depth: number; path: string; leaf: boolean }

/** The fields shown under a container: an object's children, or an array-of-object's item fields. */
const kidsOf = (f: DocField): DocField[] | null => {
  if (f.type === 'object' && (f.children?.length ?? 0) > 0) return f.children ?? []
  if (f.type === 'array' && f.items?.type === 'object' && (f.items.children?.length ?? 0) > 0) return f.items.children ?? []
  return null
}

/** Flattened target tree honouring collapsed containers and the visible leaves (embeds' items included). */
export function targetRows(spec: MappingSpec, visibleLeaves: ReadonlySet<string> | null, collapsed: ReadonlySet<string>): TargetRow[] {
  const out: TargetRow[] = []
  const hasVisible = (f: DocField): boolean => {
    const kids = kidsOf(f)
    return kids ? kids.some(hasVisible) : !visibleLeaves || visibleLeaves.has(f.id)
  }
  const visit = (fields: DocField[], depth: number, prefix: string) => {
    for (const f of fields) {
      const path = prefix ? `${prefix}.${f.name}` : f.name
      const kids = kidsOf(f)
      if (!hasVisible(f)) continue
      out.push({ field: f, depth, path, leaf: !kids })
      if (kids && !collapsed.has(f.id)) visit(kids, depth + 1, f.type === 'array' ? `${path}[]` : path)
    }
  }
  visit(spec.docSchema.root.children ?? [], 0, '')
  return out
}

/** A source panel row: a column, or the header of an embedded table's columns. */
type SourceRow = { kind: 'column'; column: CanvasColumn } | { kind: 'embed'; embed: Embed }

const stateDot: Record<SourceState | TargetState, string> = {
  auto: 'bg-[var(--map-auto)]',
  suggested: 'bg-[var(--map-suggested)]',
  manual: 'bg-[var(--map-manual)]',
  unmapped: 'border border-border-strong',
  ignored: 'bg-border-strong',
}

const edgeStyle: Record<Edge['state'], { stroke: string; dash?: string }> = {
  auto: { stroke: 'var(--map-auto)' },
  suggested: { stroke: 'var(--map-suggested)', dash: '5 4' },
  manual: { stroke: 'var(--map-manual)' },
  error: { stroke: 'var(--map-error)' },
}

type Pending = { column: CanvasColumn; mappingId: string }

const EMBED_COLORS = 6
/** The accent color of the n-th embed (tokens --embed-1…6, cycling). */
const embedVar = (n: number) => `var(--embed-${(n % EMBED_COLORS) + 1})`
/** Edge stroke for an embed's mappings: a darker shade of its accent. */
const embedEdge = (n: number) => `color-mix(in srgb, ${embedVar(n)}, black var(--embed-edge-shade))`
/** Stripe and light tint that mark an embedded table's rows in both panels. */
const embedAccent = (n: number | undefined, tint = true): CSSProperties | undefined => {
  if (n == null) return undefined
  const c = embedVar(n)
  return { boxShadow: `inset 3px 0 0 ${c}`, ...(tint ? { background: `color-mix(in srgb, ${c} 8%, transparent)` } : {}) }
}

const filtered = (filter: string, search: string) => filter !== 'all' || search.trim() !== ''

/** The mapping canvas (OC-019), with embedded tables' columns as extra source groups (OC-021). */
export function MappingCanvas() {
  const { t } = useTranslation()
  const spec = useWorkbench((s) => s.history.present)
  const tableColumns = useWorkbench((s) => s.columns)
  const embedTables = useWorkbench((s) => s.view.embedTables)
  // The table's columns, then each embedded table's (keyed "embedId:column").
  const columns = useMemo<CanvasColumn[]>(() => [...tableColumns, ...embedColumns(spec, embedTables)], [tableColumns, spec, embedTables])
  const filter = useWorkbench((s) => s.filter)
  const search = useWorkbench((s) => s.search)
  const collapsed = useWorkbench((s) => s.collapsed)
  const issues = useWorkbench((s) => s.issues)
  const selection = useWorkbench((s) => s.selection)
  const apply = useWorkbench((s) => s.apply)
  const select = useWorkbench((s) => s.select)
  const toggleCollapsed = useWorkbench((s) => s.toggleCollapsed)
  const [hover, setHover] = useState<{ kind: 'source' | 'target'; id: string } | null>(null)
  const [dragOver, setDragOver] = useState<{ id: string; ok: boolean; reason?: string } | null>(null)
  const [pending, setPending] = useState<Pending | null>(null)
  const [scroll, setScroll] = useState({ src: 0, tgt: 0 })
  const srcRef = useRef<HTMLDivElement>(null)
  const dragging = useRef<string | null>(null) // the column being dragged
  const tgtRef = useRef<HTMLDivElement>(null)

  const ls = useMemo(() => canvasLeaves(spec), [spec])
  const fields = useMemo(() => fieldsById(spec), [spec])
  const vis = useMemo(() => selectVisible(spec, columns, ls, filter, search, issues), [spec, columns, ls, filter, search, issues])
  const srcRows = useMemo<SourceRow[]>(() => {
    const shown = new Set(vis.sources)
    const rows: SourceRow[] = tableColumns.filter((c) => shown.has(c.name)).map((c) => ({ kind: 'column', column: c }))
    for (const e of spec.embeds) {
      const cols = columns.filter((c) => c.embed === e.id && shown.has(c.name))
      if (cols.length === 0 && filtered(filter, search)) continue
      rows.push({ kind: 'embed', embed: e }, ...cols.map((c) => ({ kind: 'column' as const, column: c })))
    }
    return rows
  }, [tableColumns, columns, spec.embeds, vis.sources, filter, search])
  const isFiltered = filtered(filter, search)
  const tRows = useMemo(() => targetRows(spec, isFiltered ? new Set(vis.targets) : null, collapsed), [spec, isFiltered, vis.targets, collapsed])
  const edges = useMemo(() => buildEdges(spec, collapsed, parentMap(spec), issues), [spec, collapsed, issues])
  const srcIndex = useMemo(() => new Map(srcRows.flatMap((r, i) => (r.kind === 'column' ? [[r.column.name, i] as const] : []))), [srcRows])
  const tgtIndex = useMemo(() => new Map(tRows.map((r, i) => [r.field.id, i])), [tRows])
  const shownEdges = edges.filter((e) => srcIndex.has(e.from) && tgtIndex.has(e.to))

  // TanStack Virtual is a known React Compiler exception (see SchemaTree).
  // eslint-disable-next-line react-hooks/incompatible-library
  const sv = useVirtualizer({ count: srcRows.length, getScrollElement: () => srcRef.current, estimateSize: () => ROW, overscan: 10, initialRect: { width: 280, height: 600 } })
  const tv = useVirtualizer({ count: tRows.length, getScrollElement: () => tgtRef.current, estimateSize: () => ROW, overscan: 10, initialRect: { width: 320, height: 600 } })

  // A newly added embed lands at the end of both lists: scroll both panes so its fields are in view.
  const seenEmbeds = useRef(new Set(spec.embeds.map((e) => e.id)))
  useEffect(() => {
    const fresh = spec.embeds.find((e) => !seenEmbeds.current.has(e.id))
    seenEmbeds.current = new Set(spec.embeds.map((e) => e.id))
    if (!fresh) return
    const si = srcRows.findIndex((r) => r.kind === 'embed' && r.embed.id === fresh.id)
    if (si >= 0) sv.scrollToIndex(si, { align: 'start' })
    const ti = tgtIndex.get(fresh.targetFieldId)
    if (ti != null) tv.scrollToIndex(ti, { align: 'start' })
  }, [spec.embeds, srcRows, tgtIndex, sv, tv])

  const colByName = useMemo(() => new Map(columns.map((c) => [c.name, c])), [columns])
  const related = (e: Edge) => !!hover && ((hover.kind === 'source' && e.from === hover.id) || (hover.kind === 'target' && e.to === hover.id))

  /** Connects a column to a field, handling occupied fields, lossy pairs and errors (OC-019 AC2/AC3). */
  const onConnect = (columnName: string, fieldId: string) => {
    const column = colByName.get(columnName)
    if (!column) return
    try {
      const res = connect(spec, column, fieldId)
      if (res.outcome === 'occupied') {
        setPending({ column, mappingId: res.mappingId })
        return
      }
      apply(() => res.spec)
      select({ kind: 'mapping', id: res.mappingId })
      if (res.class === 'lossy') toast.warning(t('mapping.lossyToast', { op: res.spec.fieldMappings.at(-1)?.transforms[0]?.op ?? '' }))
    } catch (e) {
      if (e instanceof OpError) toast.error(e.message)
    }
  }

  /** Dropped on empty target space or an object row: create a field there and map it (AC4). */
  const onCreateAndMap = (columnName: string, parentId: string) => {
    const column = colByName.get(columnName)
    if (!column) return
    // An embedded column dropped outside its embed goes into the embed's object.
    const embed = column.embed ? spec.embeds.find((e) => e.id === column.embed) : undefined
    const parent = fields.get(parentId)
    const into = embed ? (embedContainer(spec, embed)?.id ?? parentId) : parent?.type === 'array' && parent.items?.type === 'object' ? parent.items.id : parentId
    try {
      apply((s) => createFieldAndMap(s, column, into).spec)
    } catch (e) {
      if (e instanceof OpError) toast.error(e.message)
    }
  }

  const fieldType = (id: string) => fields.get(id)?.type
  const onTargetDragOver = (e: DragEvent, row: TargetRow) => {
    const col = colByName.get(dragging.current ?? '')
    e.preventDefault()
    if (!row.leaf) {
      setDragOver({ id: row.field.id, ok: true })
      return
    }
    const type = fieldType(row.field.id)
    const cc = col && type ? canConnect(col.jsonType, type) : null
    const ok = !!cc?.allowed
    e.dataTransfer.dropEffect = ok ? 'link' : 'none'
    setDragOver({ id: row.field.id, ok, reason: ok ? undefined : t('mapping.incompatible', { from: col?.jsonType ?? '', to: type ?? '' }) })
  }
  const onKeyDown = (e: KeyboardEvent) => {
    if ((e.key === 'Delete' || e.key === 'Backspace') && selection?.kind === 'mapping') {
      const id = selection.id
      apply((s) => disconnect(s, id))
      select(null)
      e.preventDefault()
    }
  }

  const embedTargets = useMemo(() => new Set(spec.embeds.map((e) => e.targetFieldId)), [spec.embeds])
  // Which embed (by position) each embedded column and target field belongs to, for its accent color.
  const embedColor = useMemo(() => {
    const byEmbed = new Map(spec.embeds.map((e, i) => [e.id, i]))
    const byField = new Map<string, number>()
    const mark = (f: DocField, i: number) => {
      byField.set(f.id, i)
      for (const k of kidsOf(f) ?? []) mark(k, i)
    }
    spec.embeds.forEach((e, i) => {
      const f = fields.get(e.targetFieldId)
      if (f) mark(f, i)
    })
    return { byEmbed, byField }
  }, [spec.embeds, fields])
  const colorOf = (embedId: string) => embedColor.byEmbed.get(embedId)
  const height = Math.max(srcRows.length, tRows.length) * ROW + HEADER
  const yOf = (i: number, scrollTop: number) => HEADER + i * ROW + ROW / 2 - scrollTop
  // Edges to rows scrolled out of view are pinned to the pane's visible edge instead of
  // running past it (e.g. over the Related tables panel).
  const clampY = (y: number, viewport: number | undefined) => (viewport ? Math.min(Math.max(y, HEADER), HEADER + viewport) : y)
  const srcViewport = sv.scrollRect?.height
  const tgtViewport = tv.scrollRect?.height

  return (
    <div className="relative flex min-h-0 flex-1 outline-none" tabIndex={0} onKeyDown={onKeyDown} aria-label={t('mapping.canvas')} role="application">
      {/* Source panel */}
      <div className="flex w-[300px] min-w-[200px] shrink flex-col border-r border-border">
        <div className="flex h-9 items-center border-b border-border bg-surface-muted px-3 text-xs font-medium text-muted">{t('mapping.sourcePanel', { count: srcRows.filter((r) => r.kind === 'column').length })}</div>
        <div ref={srcRef} className="min-h-0 flex-1 overflow-y-auto" onScroll={(e: UIEvent<HTMLDivElement>) => { const top = e.currentTarget.scrollTop; setScroll((s) => ({ ...s, src: top })) }}>
          <div style={{ height: sv.getTotalSize(), position: 'relative' }}>
            {sv.getVirtualItems().map((v) => {
              const row = srcRows[v.index]
              if (!row) return null
              if (row.kind === 'embed') {
                const e = row.embed
                return (
                  <div
                    key={`embed-${e.id}`}
                    data-testid={`embed-group-${e.id}`}
                    className="absolute left-0 right-0 flex items-center gap-1.5 border-y border-border bg-surface-muted px-3 text-xs font-medium text-muted"
                    style={{ top: v.start, height: ROW, ...embedAccent(colorOf(e.id)) }}
                  >
                    <CornerDownRight className="size-3.5 shrink-0" style={{ color: embedVar(colorOf(e.id) ?? 0) }} aria-hidden />
                    <span className="truncate">{t(e.kind === 'one-to-many' ? 'embed.groupMany' : 'embed.groupOne', { table: e.table.name, field: fields.get(e.targetFieldId)?.name ?? '' })}</span>
                    <Can role="editor">
                      <button
                        type="button"
                        className="ml-auto rounded p-0.5 text-subtle hover:bg-surface hover:text-foreground"
                        aria-label={t('embed.remove', { table: e.table.name })}
                        onClick={() => apply((s) => removeEmbed(s, e.id))}
                      >
                        <X className="size-3.5" />
                      </button>
                    </Can>
                  </div>
                )
              }
              const c = row.column
              const st = vis.sourceStates.get(c.name) ?? 'unmapped'
              const dim = hover && !(hover.kind === 'source' && hover.id === c.name) && !edges.some((e) => e.from === c.name && related(e))
              return (
                <div
                  key={c.name}
                  data-testid={`source-${c.name}`}
                  draggable={st !== 'ignored'}
                  onDragStart={(e) => {
                    dragging.current = c.name
                    e.dataTransfer.setData('text/x-column', c.name)
                    e.dataTransfer.effectAllowed = 'link'
                  }}
                  onDragEnd={() => {
                    dragging.current = null
                    setDragOver(null)
                  }}
                  onMouseEnter={() => setHover({ kind: 'source', id: c.name })}
                  onMouseLeave={() => setHover(null)}
                  onClick={() => select({ kind: 'column', name: c.name })}
                  className={cn(
                    'absolute left-0 right-0 flex cursor-grab items-center gap-2 px-3 text-[13px] hover:bg-surface-muted',
                    dim && 'opacity-40',
                    st === 'ignored' && 'text-subtle line-through',
                    selection?.kind === 'column' && selection.name === c.name && 'bg-primary-soft',
                  )}
                  style={{ top: v.start, height: ROW, ...embedAccent(c.embed ? colorOf(c.embed) : undefined, !(selection?.kind === 'column' && selection.name === c.name)) }}
                >
                  <span className="flex w-3.5 justify-center">{c.isPrimaryKey ? <KeyRound className="size-3.5 text-warning" aria-label={t('explorer.pk')} /> : null}</span>
                  <span className={cn('truncate font-mono text-xs', c.embed && 'pl-2')}>{c.column ?? c.name}</span>
                  <span className="ml-auto truncate font-mono text-[11px] text-subtle">{c.udtName}</span>
                  {st === 'ignored' ? <EyeOff className="size-3 text-subtle" aria-label={t('mapping.ignored')} /> : null}
                  <span className={cn('size-2.5 shrink-0 rounded-full', stateDot[st])} aria-label={t(`mapping.state.${st}`)} />
                </div>
              )
            })}
          </div>
        </div>
        <RelatedTables />
      </div>

      {/* Edges */}
      <svg className="pointer-events-none relative shrink-0 overflow-visible" width={GUTTER} height={height} aria-hidden>
        {shownEdges.map((e) => {
          const ry1 = yOf(srcIndex.get(e.from)!, scroll.src)
          const ry2 = yOf(tgtIndex.get(e.to)!, scroll.tgt)
          const y1 = clampY(ry1, srcViewport)
          const y2 = clampY(ry2, tgtViewport)
          const offscreen = y1 !== ry1 || y2 !== ry2
          // Mappings into or out of an embed take its color; errors stay red so they stand out.
          const emb = colByName.get(e.from)?.embed
          const embIdx = emb ? colorOf(emb) : embedColor.byField.get(e.to)
          const st = embIdx != null && e.state !== 'error' ? { ...edgeStyle[e.state], stroke: embedEdge(embIdx) } : edgeStyle[e.state]
          const selected = selection?.kind === 'mapping' && selection.id === e.mappingId
          const x2 = e.multi ? GUTTER - 14 : GUTTER
          const d = `M0,${y1} C${GUTTER / 2},${y1} ${x2 / 2},${y2} ${x2},${y2}`
          return (
            <g key={e.id} data-testid={`edge-${e.id}`} data-state={e.state} className={cn('pointer-events-auto cursor-pointer', related(e) && 'edge-related', hover && !related(e) ? 'opacity-25' : offscreen && 'opacity-40')}>
              <path d={d} fill="none" stroke="transparent" strokeWidth={12} onClick={() => select({ kind: 'mapping', id: e.mappingId })} />
              <path d={d} fill="none" stroke={st.stroke} strokeWidth={selected || related(e) ? 3 : 1.75} strokeDasharray={st.dash} onClick={() => select({ kind: 'mapping', id: e.mappingId })} />
              {e.multi ? <circle cx={x2} cy={y2} r={3.5} fill={st.stroke} /> : null}
              {e.multi ? <path d={`M${x2},${y2} L${GUTTER},${y2}`} stroke={st.stroke} strokeWidth={1.75} /> : null}
              {e.transformCount > 0 ? (
                <g transform={`translate(${GUTTER / 2}, ${(y1 + y2) / 2})`} data-testid={`badge-${e.id}`}>
                  <rect x={-22} y={-9} width={44} height={18} rx={9} fill="var(--surface)" stroke={st.stroke} />
                  <text textAnchor="middle" dominantBaseline="central" fontSize={10} fill="currentColor">
                    {`ⓕ ${e.firstOp?.slice(0, 5) ?? ''}`}
                  </text>
                </g>
              ) : null}
              {e.bundle > 1 ? (
                <text x={GUTTER - 4} y={y2 - 6} textAnchor="end" fontSize={10} fill="currentColor">
                  {t('mapping.bundle', { count: e.bundle })}
                </text>
              ) : null}
            </g>
          )
        })}
      </svg>

      {/* Target panel */}
      <div
        className={cn('flex min-w-[240px] flex-1 flex-col border-l border-border', dragOver?.id === '$root' && 'bg-primary-soft/40')}
        onDragOver={(e) => {
          e.preventDefault()
          setDragOver({ id: '$root', ok: true })
        }}
        onDrop={(e) => {
          e.preventDefault()
          const col = e.dataTransfer.getData('text/x-column')
          setDragOver(null)
          if (col) onCreateAndMap(col, spec.docSchema.root.id)
        }}
      >
        <div className="flex h-9 items-center border-b border-border bg-surface-muted px-3 text-xs font-medium text-muted">{t('mapping.targetPanel')}</div>
        <div ref={tgtRef} className="min-h-0 flex-1 overflow-y-auto" onScroll={(e: UIEvent<HTMLDivElement>) => { const top = e.currentTarget.scrollTop; setScroll((s) => ({ ...s, tgt: top })) }}>
          <div style={{ height: tv.getTotalSize(), position: 'relative' }}>
            {tv.getVirtualItems().map((v) => {
              const r = tRows[v.index]
              if (!r) return null
              const st = vis.targetStates.get(r.field.id)
              const over = dragOver?.id === r.field.id
              const dim = hover && !(hover.kind === 'target' && hover.id === r.field.id) && !edges.some((e) => e.to === r.field.id && related(e))
              return (
                <div
                  key={r.field.id}
                  data-testid={`target-${r.path}`}
                  title={over && !dragOver?.ok ? dragOver?.reason : undefined}
                  onDragOver={(e) => {
                    e.stopPropagation()
                    onTargetDragOver(e, r)
                  }}
                  onDragLeave={() => setDragOver(null)}
                  onDrop={(e) => {
                    e.preventDefault()
                    e.stopPropagation()
                    const col = e.dataTransfer.getData('text/x-column')
                    setDragOver(null)
                    if (!col) return
                    if (r.leaf) onConnect(col, r.field.id)
                    else onCreateAndMap(col, r.field.id)
                  }}
                  onMouseEnter={() => setHover({ kind: 'target', id: r.field.id })}
                  onMouseLeave={() => setHover(null)}
                  onClick={() => select({ kind: 'field', id: r.field.id })}
                  className={cn(
                    'absolute left-0 right-0 flex items-center gap-2 pr-3 text-[13px] hover:bg-surface-muted',
                    dim && 'opacity-40',
                    over && (dragOver?.ok ? 'ring-2 ring-inset ring-primary' : 'ring-2 ring-inset ring-danger'),
                    selection?.kind === 'field' && selection.id === r.field.id && 'bg-primary-soft',
                  )}
                  style={{ top: v.start, height: ROW, paddingLeft: 12 + r.depth * 16, ...embedAccent(embedColor.byField.get(r.field.id), !(over || (selection?.kind === 'field' && selection.id === r.field.id))) }}
                >
                  {st ? <span className={cn('size-2.5 shrink-0 rounded-full', stateDot[st])} aria-label={t(`mapping.state.${st}`)} /> : <span className="size-2.5" />}
                  {!r.leaf ? (
                    <button
                      type="button"
                      aria-label={t(collapsed.has(r.field.id) ? 'mapping.expand' : 'mapping.collapse', { name: r.field.name })}
                      onClick={(e) => {
                        e.stopPropagation()
                        toggleCollapsed(r.field.id)
                      }}
                      className="text-subtle"
                    >
                      <ChevronRight className={cn('size-3.5 transition-transform', !collapsed.has(r.field.id) && 'rotate-90')} />
                    </button>
                  ) : null}
                  <span className="truncate">{r.field.name}</span>
                  {r.field.required ? <span className="text-danger">{t('mapping.requiredMark')}</span> : null}
                  {embedTargets.has(r.field.id) ? (
                    <span className="rounded-full px-1.5 text-[10px] font-medium text-white" style={{ background: embedVar(embedColor.byField.get(r.field.id) ?? 0) }}>
                      {t('embed.badge')}
                    </span>
                  ) : null}
                  <span className="ml-auto font-mono text-[11px] text-subtle">{r.field.type === 'array' ? `array<${r.field.items?.type ?? '?'}>` : r.field.type}</span>
                </div>
              )
            })}
          </div>
          <div className="flex items-center gap-1.5 px-3 py-3 text-xs text-subtle">
            <Plus className="size-3" aria-hidden />
            {t('mapping.dropToCreate')}
          </div>
        </div>
      </div>

      <Dialog open={pending !== null} onOpenChange={(o) => !o && setPending(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('mapping.occupiedTitle')}</DialogTitle>
            <DialogDescription>{t('mapping.occupiedBody', { column: pending?.column.name ?? '' })}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="secondary"
              onClick={() => {
                if (pending) apply((s) => addSource(s, pending.mappingId, pending.column.name))
                setPending(null)
              }}
            >
              {t('mapping.addSource')}
            </Button>
            <Button
              onClick={() => {
                if (pending) {
                  try {
                    apply((s) => replaceSource(s, pending.mappingId, pending.column))
                  } catch (e) {
                    if (e instanceof OpError) toast.error(e.message)
                  }
                }
                setPending(null)
              }}
            >
              {t('mapping.replace')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
