import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Braces, ChevronDown, ChevronRight, Download, FileJson, MoreHorizontal, Plus, Upload } from 'lucide-react'
import { useVirtualizer } from '@tanstack/react-virtual'
import { memo, useMemo, useRef, useState, type DragEvent, type KeyboardEvent, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { ApiError } from '@/lib/api/client'
import { useStable } from '@/lib/useStable'
import type { NamingStyle } from '@/lib/naming'
import { cn } from '@/lib/utils'
import { exportJsonSchema, inferDocSchema, inferFromCollection, replaceDesign } from '@/features/mapping/api'
import { useWorkbench } from '@/features/mapping/store'
import type { DocField, DocSchema, FieldType, MappingSpec } from '@/features/mapping/types'
import { addField, duplicate, move, OpError, remove, rename, setRequired, setType, unwrap, validate, walk, wrap } from './docschemaOps'
import { generateFromTable, sampleDocument } from './generate'
import { schemaDiff, type SchemaDiff } from './schemaDiff'
import { sniffJson } from './sniffJson'

const TYPES: FieldType[] = ['string', 'integer', 'number', 'boolean', 'datetime', 'object', 'array', 'any']
const ROW_HEIGHT = 36
const menuItem = 'cursor-default rounded px-2 py-1.5 text-sm outline-none data-[highlighted]:bg-surface-muted'

type Row = { field: DocField; depth: number; path: string; parentId: string }

function flatten(schema: DocSchema, collapsed: ReadonlySet<string>): Row[] {
  const out: Row[] = []
  const visit = (parent: DocField, depth: number, prefix: string) => {
    for (const f of parent.children ?? []) {
      const path = prefix ? `${prefix}.${f.name}` : f.name
      out.push({ field: f, depth, path, parentId: parent.id })
      if (f.type === 'object' && !collapsed.has(f.id)) visit(f, depth + 1, path)
    }
  }
  visit(schema.root, 0, '')
  return out
}

type RowAction = 'rename' | 'addField' | 'unwrap' | 'wrap' | 'duplicate' | 'delete'

type RowProps = {
  row: Row
  top: number
  status?: 'mapped' | 'suggested' | 'unmapped'
  error?: string
  selected: boolean
  collapsed: boolean
  editing: boolean
  onSelect: (id: string, additive: boolean) => void
  onToggle: (id: string) => void
  onStartEdit: (id: string) => void
  onRename: (id: string, name: string) => void
  onType: (id: string, type: FieldType) => void
  onRequired: (id: string, v: boolean) => void
  onDrop: (draggedId: string, target: Row) => void
  onAction: (action: RowAction, row: Row) => void
}

/** Rows are rebuilt on every edit, but an untouched field keeps its object identity. */
function sameRowProps(a: RowProps, b: RowProps): boolean {
  for (const k of Object.keys(a) as (keyof RowProps)[]) {
    if (k === 'row') {
      const x = a.row
      const y = b.row
      if (x.field !== y.field || x.depth !== y.depth || x.path !== y.path || x.parentId !== y.parentId) return false
    } else if (a[k] !== b[k]) return false
  }
  return true
}

/** Mounted only while editing, so the draft always starts from the current name. */
function RenameInput({ id, name, onRename }: { id: string; name: string; onRename: (id: string, name: string) => void }) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState(name)
  return (
    <input
      autoFocus
      aria-label={t('designer.rename', { name })}
      value={draft}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={() => onRename(id, draft)}
      onKeyDown={(e) => {
        e.stopPropagation()
        if (e.key === 'Enter') onRename(id, draft)
        if (e.key === 'Escape') onRename(id, name)
      }}
      onClick={(e) => e.stopPropagation()}
      className="h-7 min-w-0 flex-1 rounded border border-border-strong bg-surface px-1.5 font-mono text-xs"
    />
  )
}

/** One design row; memoized so edits re-render only the rows they touch (OC-015 AC8). */
export const DesignRow = memo(function DesignRow(p: RowProps) {
  const { t } = useTranslation()
  const f = p.row.field
  const act = (a: RowAction) => () => p.onAction(a, p.row)
  return (
    <div
      role="treeitem"
      aria-level={p.row.depth + 1}
      aria-selected={p.selected}
      aria-expanded={f.type === 'object' ? !p.collapsed : undefined}
      data-field-id={f.id}
      draggable={!p.editing}
      onDragStart={(e) => e.dataTransfer.setData('text/x-field', f.id)}
      onDragOver={(e) => e.preventDefault()}
      onDrop={(e) => {
        const id = e.dataTransfer.getData('text/x-field')
        if (id) {
          e.preventDefault()
          e.stopPropagation()
          p.onDrop(id, p.row)
        }
      }}
      onClick={(e) => p.onSelect(f.id, e.shiftKey || e.metaKey || e.ctrlKey)}
      onDoubleClick={() => p.onStartEdit(f.id)}
      className={cn('absolute inset-x-0 flex h-9 items-center gap-2 border-b border-border pr-2 text-[13px]', p.selected && 'bg-primary-soft', p.error && 'bg-danger-soft/40')}
      style={{ top: p.top, paddingLeft: 12 + p.row.depth * 18 }}
    >
      {f.type === 'object' ? (
        <button type="button" onClick={(e) => (e.stopPropagation(), p.onToggle(f.id))} aria-label={t(p.collapsed ? 'mapping.expand' : 'mapping.collapse', { name: f.name })} className="text-subtle">
          <ChevronRight className={cn('size-3.5 transition-transform', !p.collapsed && 'rotate-90')} />
        </button>
      ) : (
        <span className="w-3.5" />
      )}
      {p.editing ? <RenameInput id={f.id} name={f.name} onRename={p.onRename} /> : <span className="min-w-0 truncate font-mono text-xs">{f.name}</span>}
      {p.error ? <span className="truncate text-xs text-danger">{p.error}</span> : null}
      <span className="ml-auto flex items-center gap-2">
        <select
          aria-label={t('designer.typeOf', { name: f.name })}
          value={f.type}
          onClick={(e) => e.stopPropagation()}
          onChange={(e) => p.onType(f.id, e.target.value as FieldType)}
          className="h-7 rounded border border-border bg-surface px-1 font-mono text-[11px]"
        >
          {TYPES.map((x) => (
            <option key={x} value={x}>
              {x === 'array' && f.type === 'array' ? `array<${f.items?.type ?? '?'}>` : x}
            </option>
          ))}
        </select>
        <label className="flex items-center gap-1 text-[11px] text-muted" onClick={(e) => e.stopPropagation()}>
          <input type="checkbox" checked={!!f.required} onChange={(e) => p.onRequired(f.id, e.target.checked)} aria-label={t('designer.requiredFor', { name: f.name })} />
          {t('designer.required')}
        </label>
        {p.status ? (
          <span className="flex w-20 items-center gap-1 text-[11px] text-muted">
            <span className={cn('size-2 rounded-full', p.status === 'mapped' ? 'bg-[var(--map-auto)]' : p.status === 'suggested' ? 'bg-[var(--map-suggested)]' : 'border border-border-strong')} />
            {t(`designer.status.${p.status}`)}
          </span>
        ) : (
          <span className="w-20" />
        )}
        <DropdownMenu.Root>
          <DropdownMenu.Trigger asChild>
            <Button variant="ghost" size="icon" className="size-7" aria-label={t('designer.actionsFor', { name: f.name })} onClick={(e) => e.stopPropagation()}>
              <MoreHorizontal aria-hidden />
            </Button>
          </DropdownMenu.Trigger>
          <DropdownMenu.Portal>
            <DropdownMenu.Content align="end" className="z-50 min-w-44 rounded-md border border-border bg-surface p-1 shadow-lg">
              <DropdownMenu.Item className={menuItem} onSelect={act('rename')}>
                {t('designer.renameAction')}
              </DropdownMenu.Item>
              {f.type === 'object' ? (
                <>
                  <DropdownMenu.Item className={menuItem} onSelect={act('addField')}>
                    {t('designer.addField')}
                  </DropdownMenu.Item>
                  <DropdownMenu.Item className={menuItem} onSelect={act('unwrap')}>
                    {t('designer.unwrap')}
                  </DropdownMenu.Item>
                </>
              ) : null}
              <DropdownMenu.Item className={menuItem} onSelect={act('wrap')}>
                {t('designer.wrap')}
              </DropdownMenu.Item>
              <DropdownMenu.Item className={menuItem} onSelect={act('duplicate')}>
                {t('designer.duplicate')}
              </DropdownMenu.Item>
              <DropdownMenu.Item className={cn(menuItem, 'text-danger')} onSelect={act('delete')}>
                {t('designer.delete')}
              </DropdownMenu.Item>
            </DropdownMenu.Content>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
      </span>
    </div>
  )
}, sameRowProps)

type Incoming = { schema: DocSchema; diff: SchemaDiff; warnings: string[] }

/** The document designer (OC-015): tree editing plus the four ways to create a design. */
export function DocDesigner({ projectId, targetConnId, header }: { projectId: string; targetConnId: string; header?: ReactNode }) {
  const { t } = useTranslation()
  const spec = useWorkbench((s) => s.history.present)
  const columns = useWorkbench((s) => s.columns)
  const view = useWorkbench((s) => s.view)
  const version = useWorkbench((s) => s.version)
  const apply = useWorkbench((s) => s.apply)
  const replace = useWorkbench((s) => s.replace)
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(new Set())
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set())
  const [editing, setEditing] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<string | null>(null)
  const [incoming, setIncoming] = useState<Incoming | null>(null)
  const [importKind, setImportKind] = useState<'json-sample' | 'json-schema' | null>(null)
  const [importText, setImportText] = useState('')
  const [generate, setGenerate] = useState<{ naming: NamingStyle; nestByPrefix: boolean } | null>(null)
  const [jsonView, setJsonView] = useState(false)
  const [busy, setBusy] = useState(false)

  const rows = useMemo(() => flatten(spec.docSchema, collapsed), [spec.docSchema, collapsed])
  const errors = useMemo(() => new Map(validate(spec.docSchema).map((i) => [i.fieldId, i.message])), [spec.docSchema])
  const status = useMemo(() => {
    const m = new Map<string, 'mapped' | 'suggested'>()
    for (const fm of spec.fieldMappings) {
      if (fm.state === 'active') m.set(fm.targetFieldId, 'mapped')
      else if (fm.state === 'pending' && !m.has(fm.targetFieldId)) m.set(fm.targetFieldId, 'suggested')
    }
    return m
  }, [spec.fieldMappings])

  const edit = (fn: (s: DocSchema) => DocSchema) => {
    try {
      apply((s) => {
        const docSchema = fn(s.docSchema)
        return docSchema === s.docSchema ? s : { ...s, docSchema }
      })
    } catch (e) {
      if (e instanceof OpError) toast.error(e.message)
      else throw e
    }
  }
  const mapped = (id: string) => spec.fieldMappings.some((m) => m.targetFieldId === id && m.state !== 'rejected')
  const doRemove = (id: string) =>
    apply((s) => ({ ...s, docSchema: remove(s.docSchema, id), fieldMappings: s.fieldMappings.filter((m) => m.targetFieldId !== id || !isInside(s.docSchema, id, m.targetFieldId)) }))

  const onSelect = useStable((id: string, additive: boolean) =>
    setSelected((prev) => {
      if (!additive) return new Set([id])
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    }),
  )

  const onRename = useStable((id: string, name: string) => {
    setEditing(null)
    if (name !== walk(spec.docSchema).find((r) => r.field.id === id)?.field.name) edit((s) => rename(s, id, name))
  })

  const onDrop = useStable((draggedId: string, target: Row) => {
    if (draggedId === target.field.id) return
    if (target.field.type === 'object') edit((s) => move(s, draggedId, target.field.id, target.field.children?.length ?? 0))
    else {
      const siblings = target.parentId === spec.docSchema.root.id ? spec.docSchema.root.children! : (walk(spec.docSchema).find((r) => r.field.id === target.parentId)?.field.children ?? [])
      edit((s) => move(s, draggedId, target.parentId, siblings.findIndex((c) => c.id === target.field.id)))
    }
  })
  const onToggle = useStable((id: string) =>
    setCollapsed((c) => {
      const next = new Set(c)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    }),
  )
  const onType = useStable((id: string, type: FieldType) => edit((s) => setType(s, id, type)))
  const onRequired = useStable((id: string, v: boolean) => edit((s) => setRequired(s, id, v)))
  const onAction = useStable((action: RowAction, row: Row) => {
    const id = row.field.id
    if (action === 'rename') setEditing(id)
    else if (action === 'addField') edit((s) => addField(s, id, 'field', 'string').schema)
    else if (action === 'unwrap') edit((s) => unwrap(s, id))
    else if (action === 'wrap') edit((s) => wrap(s, selected.has(id) ? [...selected] : [id], 'group').schema)
    else if (action === 'duplicate') edit((s) => duplicate(s, id).schema)
    else if (mapped(id)) setDeleting(id)
    else doRemove(id)
  })

  const scrollRef = useRef<HTMLDivElement>(null)
  // eslint-disable-next-line react-hooks/incompatible-library
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ROW_HEIGHT,
    getItemKey: (i) => rows[i]!.field.id,
    overscan: 12,
    initialRect: { width: 600, height: 600 },
  })

  const onKeyDown = (e: KeyboardEvent) => {
    const [only] = [...selected]
    if (!only || editing) return
    if (e.key === 'F2') {
      setEditing(only)
      e.preventDefault()
    } else if (e.key === 'Delete' || e.key === 'Backspace') {
      if (mapped(only)) setDeleting(only)
      else doRemove(only)
      e.preventDefault()
    }
  }

  /** A new design arrived (import, sample, generate): show the diff, then replace or merge. */
  const offer = (schema: DocSchema, warnings: string[] = []) => setIncoming({ schema, diff: schemaDiff(spec.docSchema, schema), warnings })

  const applyIncoming = async (strategy: 'replace' | 'merge') => {
    if (!incoming) return
    setBusy(true)
    try {
      // Save pending local edits first isn't needed: the server reattaches onto its stored spec.
      const res = await replaceDesign(projectId, view.id, version, incoming.schema, strategy)
      replace(res.mapping)
      if (res.orphans.length) toast.warning(t('designer.orphans', { count: res.orphans.length }))
      else toast.success(t('designer.applied'))
      setIncoming(null)
    } catch (e) {
      toast.error(e instanceof ApiError ? (e.detail ?? e.title) : String(e))
    } finally {
      setBusy(false)
    }
  }

  const infer = async (kind: 'json-sample' | 'json-schema', text: string) => {
    try {
      const res = await inferDocSchema(kind, text)
      offer(res.docSchema, res.warnings)
      setImportKind(null)
      setImportText('')
    } catch (e) {
      toast.error(e instanceof ApiError ? (e.detail ?? e.title) : String(e))
    }
  }

  const onFileDrop = async (e: DragEvent) => {
    const file = e.dataTransfer.files?.[0]
    if (!file) return
    e.preventDefault()
    const text = await file.text()
    const sniff = sniffJson(text)
    if (sniff.error || !sniff.kind) {
      toast.error(sniff.error ?? t('designer.unrecognized'))
      return
    }
    await infer(sniff.kind, text)
  }

  return (
    <section
      aria-label={t('designer.title')}
      className="flex min-h-0 flex-1 flex-col"
      onDragOver={(e) => e.dataTransfer.types.includes('Files') && e.preventDefault()}
      onDrop={(e) => void onFileDrop(e)}
    >
      {header}
      <div className="flex flex-wrap items-center gap-2 border-b border-border px-6 py-2">
        <h3 className="text-sm font-semibold">{t('designer.title')}</h3>
        <Badge tone="neutral">{t(`designer.source.${spec.docSchema.source}`)}</Badge>
        <div className="ml-auto flex gap-2">
          <DropdownMenu.Root>
            <DropdownMenu.Trigger asChild>
              <Button variant="secondary" size="sm">
                <Braces aria-hidden />
                {t('designer.generate')}
                <ChevronDown aria-hidden />
              </Button>
            </DropdownMenu.Trigger>
            <DropdownMenu.Portal>
              <DropdownMenu.Content align="end" className="z-50 min-w-56 rounded-md border border-border bg-surface p-1 shadow-lg">
                <DropdownMenu.Item className={menuItem} onSelect={() => setGenerate({ naming: 'camelCase', nestByPrefix: false })}>
                  {t('designer.fromTable')}
                </DropdownMenu.Item>
                <DropdownMenu.Item
                  className={menuItem}
                  onSelect={() =>
                    void inferFromCollection(targetConnId, view.target)
                      .then((r) => offer(r.docSchema, r.warnings))
                      .catch((e: unknown) => toast.error(e instanceof ApiError ? (e.detail ?? e.title) : String(e)))
                  }
                >
                  {t('designer.fromCollection')}
                </DropdownMenu.Item>
              </DropdownMenu.Content>
            </DropdownMenu.Portal>
          </DropdownMenu.Root>
          <DropdownMenu.Root>
            <DropdownMenu.Trigger asChild>
              <Button variant="secondary" size="sm">
                <Upload aria-hidden />
                {t('designer.import')}
                <ChevronDown aria-hidden />
              </Button>
            </DropdownMenu.Trigger>
            <DropdownMenu.Portal>
              <DropdownMenu.Content align="end" className="z-50 min-w-44 rounded-md border border-border bg-surface p-1 shadow-lg">
                <DropdownMenu.Item className={menuItem} onSelect={() => setImportKind('json-sample')}>
                  {t('designer.importSample')}
                </DropdownMenu.Item>
                <DropdownMenu.Item className={menuItem} onSelect={() => setImportKind('json-schema')}>
                  {t('designer.importSchema')}
                </DropdownMenu.Item>
              </DropdownMenu.Content>
            </DropdownMenu.Portal>
          </DropdownMenu.Root>
          <Button variant="ghost" size="sm" aria-pressed={jsonView} onClick={() => setJsonView((v) => !v)}>
            <FileJson aria-hidden />
            {t('designer.jsonView')}
          </Button>
        </div>
      </div>
      {jsonView ? (
        <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-auto px-6 py-4">
          <pre className="rounded-lg bg-surface-muted p-4 font-mono text-xs">{JSON.stringify(sampleDocument(spec.docSchema, spec.typeField), null, 2)}</pre>
          <Button
            variant="secondary"
            size="sm"
            className="self-start"
            onClick={() =>
              void exportJsonSchema(spec.docSchema).then((text) => {
                const url = URL.createObjectURL(new Blob([text], { type: 'application/schema+json' }))
                const a = document.createElement('a')
                a.href = url
                a.download = `${view.sourceTable}.schema.json`
                a.click()
                URL.revokeObjectURL(url)
              })
            }
          >
            <Download aria-hidden />
            {t('designer.exportSchema')}
          </Button>
        </div>
      ) : (
        // The tree holds only its rows (ARIA: a tree owns treeitems); "Add field" follows it in the
        // same scroll area.
        <div ref={scrollRef} className="min-h-0 flex-1 overflow-y-auto">
          <div
            role="tree"
            aria-label={t('designer.tree')}
            aria-multiselectable
            tabIndex={0}
            onKeyDown={onKeyDown}
            className="relative outline-none focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-ring"
            style={{ height: virtualizer.getTotalSize() }}
          >
            {virtualizer.getVirtualItems().map((v) => {
              const r = rows[v.index]!
              return (
                <DesignRow
                  key={r.field.id}
                  row={r}
                  top={v.start}
                  status={r.field.type === 'object' && (r.field.children?.length ?? 0) > 0 ? undefined : (status.get(r.field.id) ?? 'unmapped')}
                  error={errors.get(r.field.id)}
                  selected={selected.has(r.field.id)}
                  collapsed={collapsed.has(r.field.id)}
                  editing={editing === r.field.id}
                  onSelect={onSelect}
                  onToggle={onToggle}
                  onStartEdit={setEditing}
                  onRename={onRename}
                  onType={onType}
                  onRequired={onRequired}
                  onDrop={onDrop}
                  onAction={onAction}
                />
              )
            })}
          </div>
          <div className="flex items-center gap-3 px-6 py-3">
            <Button variant="link" size="sm" className="px-0" onClick={() => edit((s) => addField(s, s.root.id, 'newField', 'string').schema)}>
              <Plus aria-hidden />
              {t('designer.addField')}
            </Button>
            <span className="text-xs text-subtle">{t('designer.dropHint')}</span>
          </div>
        </div>
      )}

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={t('designer.deleteMappedTitle')}
        description={t('designer.deleteMappedBody')}
        confirmLabel={t('designer.delete')}
        destructive
        onConfirm={() => {
          if (deleting) doRemove(deleting)
          setDeleting(null)
        }}
      />

      <Dialog open={importKind !== null} onOpenChange={(o) => !o && setImportKind(null)}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>{t(importKind === 'json-schema' ? 'designer.importSchema' : 'designer.importSample')}</DialogTitle>
            <DialogDescription>{t('designer.importBody')}</DialogDescription>
          </DialogHeader>
          <textarea
            aria-label={t('designer.pasteJson')}
            value={importText}
            onChange={(e) => setImportText(e.target.value)}
            rows={12}
            spellCheck={false}
            className="w-full rounded-[var(--radius-control)] border border-border-strong bg-surface p-2 font-mono text-xs"
          />
          <input
            type="file"
            accept=".json,.ndjson,application/json"
            aria-label={t('designer.chooseFile')}
            onChange={(e) => void e.target.files?.[0]?.text().then(setImportText)}
            className="text-xs"
          />
          <DialogFooter>
            <Button variant="ghost" onClick={() => setImportKind(null)}>
              {t('common.cancel')}
            </Button>
            <Button disabled={!importText.trim()} onClick={() => importKind && void infer(importKind, importText)}>
              {t('designer.preview')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={generate !== null} onOpenChange={(o) => !o && setGenerate(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('designer.fromTable')}</DialogTitle>
            <DialogDescription>{t('designer.fromTableBody')}</DialogDescription>
          </DialogHeader>
          {generate ? (
            <div className="flex flex-col gap-3 text-[13px]">
              <label className="flex items-center gap-2">
                {t('designer.naming')}
                <select value={generate.naming} onChange={(e) => setGenerate({ ...generate, naming: e.target.value as NamingStyle })} className="h-8 rounded border border-border-strong bg-surface px-2">
                  <option value="camelCase">{t('designer.camel')}</option>
                  <option value="snake_case">{t('designer.snake')}</option>
                  <option value="preserve">{t('designer.preserve')}</option>
                </select>
              </label>
              <label className="flex items-center gap-2">
                <input type="checkbox" checked={generate.nestByPrefix} onChange={(e) => setGenerate({ ...generate, nestByPrefix: e.target.checked })} />
                {t('designer.nestByPrefix')}
              </label>
            </div>
          ) : null}
          <DialogFooter>
            <Button
              onClick={() => {
                if (generate) offer(generateFromTable(columns, generate))
                setGenerate(null)
              }}
            >
              {t('designer.preview')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={incoming !== null} onOpenChange={(o) => !o && setIncoming(null)}>
        <DialogContent className="max-w-xl">
          <DialogHeader>
            <DialogTitle>{t('designer.diffTitle')}</DialogTitle>
            <DialogDescription>{t('designer.diffBody')}</DialogDescription>
          </DialogHeader>
          {incoming ? (
            <div className="flex max-h-80 flex-col gap-1 overflow-y-auto font-mono text-xs">
              {incoming.diff.added.map((p) => (
                <span key={`+${p}`} className="text-success">
                  + {p}
                </span>
              ))}
              {incoming.diff.removed.map((p) => (
                <span key={`-${p}`} className="text-danger">
                  − {p}
                </span>
              ))}
              {incoming.diff.typeChanged.map((c) => (
                <span key={`~${c.path}`} className="text-warning">
                  ~ {c.path}: {c.from} → {c.to}
                </span>
              ))}
              {incoming.warnings.map((w) => (
                <span key={w} className="text-muted">
                  ⚠ {w}
                </span>
              ))}
            </div>
          ) : null}
          <DialogFooter>
            <Button variant="secondary" loading={busy} onClick={() => void applyIncoming('merge')}>
              {t('designer.merge')}
            </Button>
            <Button loading={busy} onClick={() => void applyIncoming('replace')}>
              {t('designer.replace')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  )
}

/** Whether target lies within the field `id` (deleting an object drops its children's mappings). */
function isInside(schema: DocSchema, id: string, target: string): boolean {
  const ref = walk(schema).find((r) => r.field.id === id)
  if (!ref) return false
  const visit = (f: DocField): boolean => f.id === target || (f.children ?? []).some(visit) || (!!f.items && visit(f.items))
  return visit(ref.field)
}

export type { MappingSpec }
