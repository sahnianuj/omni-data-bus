import { AlertTriangle, Braces, Brackets, ChevronDown, ChevronRight, Hash, Link2, MoreHorizontal, OctagonAlert, Plus, ToggleLeft, Type as TypeIcon, Calendar, CircleDashed } from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type DragEvent, type KeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { insertionPoint, moveRelative, type DropPos } from '@/features/docschema/docschemaOps'
import { DND_FIELD, dropIndicator, dropPosition, isFieldDrag, startFieldDrag } from '@/features/docschema/fieldDrag'
import { activeFor } from '@/features/mapping/mappingOps'
import { useWorkbench } from '@/features/mapping/store'
import type { DocField, MappingIssue, MappingSpec } from '@/features/mapping/types'
import { cn } from '@/lib/utils'
import { addNode, deleteNode, moveNode, OpError, renameNode, unbind, type NodeKind, type TableRef } from './composerOps'
import { boundRelations } from './scope'

/** Drag payloads between the Sources pane and the tree. */
export const DND_TABLE = 'application/x-odb-table'
export const DND_COLUMN = 'application/x-odb-column'
/** The group's rows (OC-063), dragged from the Sources pane. */
export const DND_GROUP = 'application/x-odb-group'

type Row = { field: DocField; depth: number; path: string; parentId: string; expandable: boolean; item: boolean }

type Props = {
  /** Opens the table picker to bind a node (the keyboard equivalent of dropping a table). */
  onBindTable: (nodeId: string, lookup?: boolean) => void
  /** A table was dropped on a node. */
  onDropTable: (nodeId: string, table: TableRef, lookup: boolean) => void
  /** A column was dropped on a field. */
  onDropColumn: (fieldId: string, column: { relation: string; column: string; from?: 'via' }) => void
  /** "Map from column…": focus the inspector's source picker for the field. */
  onMapColumn: (fieldId: string) => void
  onPaste: (nodeId: string) => void
  /** A .json file was dropped on a node (null: the root, which replaces the design). */
  onDropFile?: (nodeId: string | null, file: File) => void
  /** Fill a node with the group's rows (dropped, or from its menu; OC-063). */
  onGroupRows?: (nodeId: string) => void
  /** The table a field's foreign key refers to, when it's on one (OC-064). */
  embeddable?: (fieldId: string) => string | undefined
  onEmbed?: (fieldId: string) => void
  /** Whether an object can collapse to its key, and doing it (OC-064). */
  collapsible?: (nodeId: string) => boolean
  onCollapse?: (nodeId: string) => void
}

const typeIcon: Record<string, typeof Hash> = {
  object: Braces, array: Brackets, integer: Hash, number: Hash, string: TypeIcon, boolean: ToggleLeft, datetime: Calendar, any: CircleDashed,
}

/** The visible rows in order (an array's item object is shown as "{ }" under it). */
function rowsOf(spec: MappingSpec, collapsed: ReadonlySet<string>): Row[] {
  const out: Row[] = []
  const visit = (parent: DocField, depth: number, prefix: string) => {
    for (const f of parent.children ?? []) {
      const path = prefix ? `${prefix}.${f.name}` : f.name
      const expandable = (f.type === 'object' && (f.children?.length ?? 0) > 0) || (f.type === 'array' && !!f.items && (f.items.type === 'object' || f.items.type === 'array'))
      out.push({ field: f, depth, path, parentId: parent.id, expandable, item: false })
      if (!expandable || collapsed.has(f.id)) continue
      if (f.type === 'object') visit(f, depth + 1, path)
      else if (f.items) {
        out.push({ field: f.items, depth: depth + 1, path: `${path}[]`, parentId: f.id, expandable: false, item: true })
        visit(f.items, depth + 2, `${path}[]`)
      }
    }
  }
  out.push({ field: spec.docSchema.root, depth: 0, path: '$', parentId: '', expandable: false, item: false })
  visit(spec.docSchema.root, 1, '')
  return out
}

/** A short label of a field's source: "← order_id", "← customers.name", "ƒ expression", "= constant". */
function sourceLabel(spec: MappingSpec, fieldId: string): string | undefined {
  const m = activeFor(spec, fieldId)
  if (!m) return undefined
  if (m.transforms.some((s) => s.op === 'expression')) return 'ƒ'
  if (m.sources.length === 0) return '='
  return m.sources
    .map((s) => {
      const r = s.relation ? spec.relations.find((x) => x.id === s.relation) : undefined
      return r ? `${s.from === 'via' ? r.via?.table.name : r.table.name}.${s.column}` : s.column
    })
    .join(' + ')
}

/**
 * The Document pane (OC-048): the composed JSON as an ARIA tree. Nodes show ⛓ their bound
 * table, ← their source, and ⚠/⛔ their issues (OC-049). Keyboard: arrows move and expand,
 * Enter adds a sibling field, Tab a child field (objects and arrays), Alt+↑/↓ or a drag reorders, F2
 * renames, Delete removes (Ctrl+Z undoes); every drag has a menu equivalent.
 */
export function DocumentTree({ onBindTable, onDropTable, onDropColumn, onMapColumn, onPaste, onDropFile, onGroupRows, embeddable, onEmbed, collapsible, onCollapse }: Props) {
  const { t } = useTranslation()
  const spec = useWorkbench((s) => s.history.present)
  const selection = useWorkbench((s) => s.selection)
  const select = useWorkbench((s) => s.select)
  const apply = useWorkbench((s) => s.apply)
  const collapsed = useWorkbench((s) => s.collapsed)
  const toggle = useWorkbench((s) => s.toggleCollapsed)
  const issues = useWorkbench((s) => s.issues)
  const rows = useMemo(() => rowsOf(spec, collapsed), [spec, collapsed])
  const bound = useMemo(() => boundRelations(spec), [spec])
  const grouped = !!spec.root?.groupBy?.length
  const selected = selection?.kind === 'field' ? selection.id : spec.docSchema.root.id
  const [editing, setEditing] = useState<string | null>(null)
  const [dropOn, setDropOn] = useState<string | null>(null)
  // Where a target field being dragged would land.
  const [dropAt, setDropAt] = useState<{ id: string; pos: DropPos } | null>(null)
  const [menuFor, setMenuFor] = useState<string | null>(null)
  const refs = useRef(new Map<string, HTMLLIElement>())

  const issuesOf = useMemo(() => {
    const byNode = new Map<string, MappingIssue[]>()
    const pathToId = new Map(rows.map((r) => [r.path, r.field.id]))
    // A field of the imported design that's gone shows on its nearest parent (OC-071).
    const nearest = (p: string): string | undefined => {
      for (let q = p; ; ) {
        const id = pathToId.get(q)
        if (id) return id
        const up = q.replace(/(\.[^.]*|\[\])$/, '')
        if (up === q) return spec.docSchema.root.id
        q = up
      }
    }
    for (const i of issues) {
      const id = i.fieldId ?? (i.nodePath ? (i.code.startsWith('REFERENCE_') ? nearest(i.nodePath) : pathToId.get(i.nodePath)) : undefined)
      if (!id) continue
      byNode.set(id, [...(byNode.get(id) ?? []), i])
    }
    return byNode
  }, [issues, rows, spec.docSchema.root.id])

  // Keep the focused row in view and focused after edits (roving tabindex).
  useEffect(() => {
    if (editing) return
    const el = refs.current.get(selected)
    if (el && el.contains(document.activeElement) === false && document.activeElement?.closest('[role="tree"]')) el.focus()
  }, [selected, editing, rows])

  const run = (fn: (s: MappingSpec) => MappingSpec) => {
    try {
      apply(fn)
    } catch (e) {
      if (e instanceof OpError) toast.error(e.message)
      else throw e
    }
  }
  const add = (parentId: string, kind: NodeKind, index?: number) => {
    try {
      let id = ''
      apply((s) => {
        const r = addNode(s, parentId, kind, t(`composer.newName.${kind}`), index)
        id = r.id
        return r.spec
      })
      select({ kind: 'field', id })
      setEditing(id)
    } catch (e) {
      if (e instanceof OpError) toast.error(e.message)
      else throw e
    }
  }
  const containerFor = (row: Row) => (row.item ? row.parentId : row.field.id)

  const onKey = (e: KeyboardEvent<HTMLUListElement>) => {
    if (editing) return
    const at = rows.findIndex((r) => r.field.id === selected)
    const row = rows[at]
    if (!row) return
    const go = (i: number) => {
      const r = rows[Math.max(0, Math.min(rows.length - 1, i))]
      if (r) select({ kind: 'field', id: r.field.id })
    }
    const isRoot = row.depth === 0
    const isContainer = row.field.type === 'object' || row.field.type === 'array'
    switch (e.key) {
      case 'ArrowDown':
        if (e.altKey && !isRoot && !row.item) run((s) => moveNode(s, row.field.id, 1))
        else go(at + 1)
        break
      case 'ArrowUp':
        if (e.altKey && !isRoot && !row.item) run((s) => moveNode(s, row.field.id, -1))
        else go(at - 1)
        break
      case 'ArrowRight':
        if (row.expandable && collapsed.has(row.field.id)) toggle(row.field.id)
        else go(at + 1)
        break
      case 'ArrowLeft':
        if (row.expandable && !collapsed.has(row.field.id)) toggle(row.field.id)
        else {
          const parent = rows.findIndex((r) => r.field.id === row.parentId)
          if (parent >= 0) go(parent)
        }
        break
      case 'Home':
        go(0)
        break
      case 'End':
        go(rows.length - 1)
        break
      case 'Enter':
        if (isRoot || row.item) add(containerFor(row), 'field')
        else {
          const siblings = rows.find((r) => r.field.id === row.parentId)
          const index = (siblings?.field.children ?? []).findIndex((c) => c.id === row.field.id)
          add(row.parentId, 'field', index + 1)
        }
        break
      case 'Tab':
        if (e.shiftKey || !(isContainer || isRoot)) return
        add(containerFor(row), 'field')
        break
      case 'F2':
        if (!isRoot && !row.item) setEditing(row.field.id)
        break
      case 'F10':
        if (!e.shiftKey || row.item) return
        setMenuFor(row.field.id)
        break
      case 'ContextMenu':
        if (row.item) return
        setMenuFor(row.field.id)
        break
      case 'Delete':
      case 'Backspace':
        if (isRoot || row.item) return
        run((s) => deleteNode(s, row.field.id))
        go(at - 1)
        break
      default:
        return
    }
    e.preventDefault()
  }

  // The root and an array's item row only take fields inside; containers inside, before or after.
  const fieldDropPos = (e: DragEvent, row: Row): DropPos => (row.depth === 0 || row.item ? 'inside' : dropPosition(e, row.field.type === 'object' || row.field.type === 'array'))
  const onDragOver = (e: DragEvent, row: Row) => {
    if (isFieldDrag(e)) {
      e.preventDefault()
      const pos = fieldDropPos(e, row)
      setDropAt((d) => (d?.id === row.field.id && d.pos === pos ? d : { id: row.field.id, pos }))
      return
    }
    const types = e.dataTransfer.types
    const container = row.field.type === 'object' || row.field.type === 'array' || row.depth === 0
    const groupTarget = types.includes(DND_GROUP) && (row.field.type === 'object' || row.field.type === 'array') && row.depth > 0 && !row.item
    if ((types.includes(DND_TABLE) && !row.item) || (types.includes(DND_COLUMN) && !container) || (types.includes('Files') && container && !row.item) || groupTarget) {
      e.preventDefault()
      setDropOn(row.field.id)
    }
  }
  const onDrop = (e: DragEvent, row: Row) => {
    e.preventDefault()
    setDropOn(null)
    setDropAt(null)
    const moved = e.dataTransfer.getData(DND_FIELD)
    if (moved) {
      const pos = fieldDropPos(e, row)
      run((s) => {
        const docSchema = moveRelative(s.docSchema, moved, row.field.id, pos)
        return docSchema === s.docSchema ? s : { ...s, docSchema: { ...docSchema, source: 'manual' } }
      })
      return
    }
    const file = e.dataTransfer.files?.[0]
    if (file && onDropFile) {
      e.stopPropagation()
      onDropFile(row.depth === 0 ? null : row.field.id, file)
      return
    }
    if (e.dataTransfer.getData(DND_GROUP) && onGroupRows) {
      onGroupRows(row.field.id)
      return
    }
    const table = e.dataTransfer.getData(DND_TABLE)
    if (table) {
      const bindable = (row.field.type === 'object' || row.field.type === 'array') && row.depth > 0
      onDropTable(row.field.id, JSON.parse(table) as TableRef, !bindable)
      return
    }
    const col = e.dataTransfer.getData(DND_COLUMN)
    if (col) onDropColumn(row.field.id, JSON.parse(col) as { relation: string; column: string; from?: 'via' })
  }

  const addTarget = insertionPoint(spec.docSchema, selected)
  const addHere = () => {
    // Open the container first (an item object's array is the row that collapses).
    const owner = rows.find((r) => r.field.id === addTarget.parentId || (r.field.items?.id === addTarget.parentId && !r.item))?.field.id
    if (owner && collapsed.has(owner)) toggle(owner)
    add(addTarget.parentId, 'field', addTarget.index)
  }

  return (
    <>
      <ul role="tree" aria-label={t('composer.document')} onKeyDown={onKey} className="flex flex-col py-1 text-[13px]">
        {rows.map((row) => {
          const f = row.field
          const Icon = typeIcon[f.type] ?? CircleDashed
          const rel = bound.get(f.id)
          const src = row.depth > 0 && !row.item ? sourceLabel(spec, f.id) : undefined
          const found = issuesOf.get(f.id) ?? []
          const errors = found.filter((i) => i.severity === 'error').length
          const warnings = found.filter((i) => i.severity === 'warning').length
          const isSel = f.id === selected
          const isContainer = f.type === 'object' || f.type === 'array'
          const name = row.depth === 0 ? '$' : row.item ? t('composer.item') : f.name
          const renamable = row.depth > 0 && !row.item
          return (
            <li
              key={f.id}
              ref={(el) => {
                if (el) refs.current.set(f.id, el)
                else refs.current.delete(f.id)
              }}
              role="treeitem"
              aria-level={row.depth + 1}
              aria-selected={isSel}
              aria-expanded={row.expandable ? !collapsed.has(f.id) : undefined}
              aria-label={[name, f.type, rel ? t('composer.boundTo', { table: rel.table.name }) : '', src ? t('composer.mappedFrom', { source: src }) : '', errors ? t('composer.errors', { count: errors }) : '', warnings ? t('composer.warnings', { count: warnings }) : ''].filter(Boolean).join(', ')}
              tabIndex={isSel ? 0 : -1}
              data-node={f.id}
              onClick={() => select({ kind: 'field', id: f.id })}
              onDoubleClick={() => renamable && setEditing(f.id)}
              draggable={renamable && editing !== f.id}
              onDragStart={(e) => startFieldDrag(e, f.id)}
              onDragEnd={() => setDropAt(null)}
              onDragOver={(e) => onDragOver(e, row)}
              onDragLeave={() => {
                setDropOn((d) => (d === f.id ? null : d))
                setDropAt((d) => (d?.id === f.id ? null : d))
              }}
              onDrop={(e) => onDrop(e, row)}
              className={cn(
                'group flex h-8 cursor-default items-center gap-1.5 pr-2 outline-none focus-visible:ring-2 focus-visible:ring-primary/60',
                isSel ? 'bg-primary-soft' : 'hover:bg-surface-muted',
                dropOn === f.id && 'ring-2 ring-primary',
                dropAt?.id === f.id && dropIndicator[dropAt.pos],
              )}
              style={{ paddingLeft: 8 + row.depth * 16 }}
            >
              {row.expandable ? (
                <button type="button" tabIndex={-1} aria-hidden onClick={(e) => { e.stopPropagation(); toggle(f.id) }} className="text-subtle">
                  {collapsed.has(f.id) ? <ChevronRight className="size-3.5" /> : <ChevronDown className="size-3.5" />}
                </button>
              ) : (
                <span className="w-3.5" />
              )}
              {errors || warnings ? (
                // In front of the name, with what's wrong and how to fix it (OC-071).
                <span role="img" className="shrink-0" title={found.map((i) => i.message).join('\n')} aria-label={found.map((i) => i.message).join(' ')}>
                  {errors ? <OctagonAlert className="size-3.5 text-danger" /> : <AlertTriangle className="size-3.5 text-warning" />}
                </span>
              ) : null}
              <Icon className="size-3.5 shrink-0 text-subtle" aria-hidden />
              {editing === f.id ? (
                <RenameInput
                  name={f.name}
                  label={t('composer.renameField', { name: f.name })}
                  onDone={(next) => {
                    setEditing(null)
                    if (next && next !== f.name) run((s) => renameNode(s, f.id, next))
                    refs.current.get(f.id)?.focus()
                  }}
                />
              ) : (
                <span
                  // The selected field's name renames on click (OC-145); the first click only selects.
                  title={isSel && renamable ? t('designer.clickToRename') : undefined}
                  onClick={(e) => {
                    if (!isSel || !renamable) return
                    e.stopPropagation()
                    setEditing(f.id)
                  }}
                  className={cn('truncate', !renamable ? 'text-muted' : 'font-medium', isSel && renamable && 'cursor-text')}
                >
                  {name}
                </span>
              )}
              {rel ? (
                <span className="flex items-center gap-0.5 rounded bg-primary-soft px-1 text-xs text-primary-soft-foreground" aria-hidden>
                  <Link2 className="size-3" />
                  {rel.group ? t('composer.groupRowsBadge') : rel.table.name}
                  {f.binding?.adapter ? <span className="text-muted">({f.binding.adapter})</span> : null}
                </span>
              ) : null}
              {src ? (
                <span className="truncate font-mono text-xs text-muted" aria-hidden>
                  ← {src}
                </span>
              ) : f.example !== undefined ? (
                <span className="truncate font-mono text-xs text-muted" aria-hidden title={t('composer.example')}>
                  · {JSON.stringify(f.example)}
                </span>
              ) : null}
              {row.item ? null : (
                <NodeMenu
                  row={row}
                  open={menuFor === f.id}
                  onOpenChange={(o) => {
                    setMenuFor(o ? f.id : null)
                    // Back to the row, unless the action focused something (a new node's name).
                    if (!o) setTimeout(() => (document.activeElement === document.body || !document.activeElement) && refs.current.get(f.id)?.focus(), 0)
                  }}
                  isContainer={isContainer}
                  bound={!!rel}
                  onAdd={(kind) => add(containerFor(row), kind)}
                  onRename={() => setEditing(f.id)}
                  onBind={() => onBindTable(f.id, !(isContainer && row.depth > 0))}
                  onUnbind={() => run((s) => unbind(s, f.id))}
                  onMap={() => onMapColumn(f.id)}
                  onPaste={() => onPaste(f.id)}
                  onGroupRows={grouped && onGroupRows ? () => onGroupRows(f.id) : undefined}
                  embedTable={embeddable?.(f.id)}
                  onEmbed={onEmbed ? () => onEmbed(f.id) : undefined}
                  onCollapse={onCollapse && collapsible?.(f.id) ? () => onCollapse(f.id) : undefined}
                  onDelete={() => run((s) => deleteNode(s, f.id))}
                />
              )}
            </li>
          )
        })}
      </ul>
      {/* Outside the tree (a tree owns only treeitems): adds where OC-081 puts it. */}
      <button type="button" onClick={addHere} className="flex h-7 items-center gap-1.5 px-3 text-xs font-medium text-primary hover:underline">
        <Plus className="size-3.5" aria-hidden />
        {addTarget.label ? t('composer.addFieldTo', { name: addTarget.label }) : t('composer.addField')}
      </button>
    </>
  )
}

function RenameInput({ name, label, onDone }: { name: string; label: string; onDone: (name: string | null) => void }) {
  const [value, setValue] = useState(name)
  return (
    <input
      autoFocus
      onFocus={(e) => e.currentTarget.select()}
      aria-label={label}
      value={value}
      onChange={(e) => setValue(e.target.value)}
      onClick={(e) => e.stopPropagation()}
      onKeyDown={(e) => {
        e.stopPropagation()
        if (e.key === 'Enter') onDone(value.trim())
        if (e.key === 'Escape') onDone(null)
      }}
      onBlur={() => onDone(value.trim())}
      className="h-6 min-w-0 flex-1 rounded border border-primary bg-surface px-1 text-[13px]"
    />
  )
}

type MenuProps = {
  row: Row
  open: boolean
  onOpenChange: (open: boolean) => void
  isContainer: boolean
  bound: boolean
  onAdd: (kind: NodeKind) => void
  onRename: () => void
  onBind: () => void
  onUnbind: () => void
  onMap: () => void
  onPaste: () => void
  onGroupRows?: () => void
  embedTable?: string
  onEmbed?: () => void
  onCollapse?: () => void
  onDelete: () => void
}

/**
 * A node's actions: the menu equivalent of every drag (OC-048 accessibility). Shift+F10 or the
 * Menu key opens it from the tree; arrows move between its items.
 */
function NodeMenu({ row, open, onOpenChange: setOpen, isContainer, bound, onAdd, onRename, onBind, onUnbind, onMap, onPaste, onGroupRows, embedTable, onEmbed, onCollapse, onDelete }: MenuProps) {
  const { t } = useTranslation()
  const isRoot = row.depth === 0
  const item = (label: string, fn: () => void, danger?: boolean) => (
    <button
      type="button"
      role="menuitem"
      onClick={() => {
        setOpen(false)
        fn()
      }}
      className={cn('w-full rounded px-2 py-1.5 text-left text-[13px] hover:bg-surface-muted focus-visible:bg-surface-muted focus-visible:outline-none', danger && 'text-danger')}
    >
      {label}
    </button>
  )
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          tabIndex={-1}
          aria-label={t('composer.actionsFor', { name: isRoot ? '$' : row.field.name })}
          onClick={(e) => e.stopPropagation()}
          onKeyDown={(e) => e.stopPropagation()}
          className="ml-auto rounded p-0.5 text-subtle opacity-0 hover:bg-surface-muted group-hover:opacity-100 group-focus:opacity-100 aria-expanded:opacity-100"
        >
          <MoreHorizontal className="size-3.5" aria-hidden />
        </button>
      </PopoverTrigger>
      {/* React events bubble out of the portal to the row: keep clicks here from selecting it. */}
      <PopoverContent
        align="end"
        className="w-52 p-1"
        onCloseAutoFocus={(e) => e.preventDefault()}
        onOpenAutoFocus={(e) => {
          e.preventDefault()
          ;(e.currentTarget as HTMLElement).querySelector<HTMLElement>('[role="menuitem"]')?.focus()
        }}
        onKeyDown={(e) => {
          e.stopPropagation()
          if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return
          e.preventDefault()
          const items = [...e.currentTarget.querySelectorAll<HTMLElement>('[role="menuitem"]')]
          const at = items.indexOf(document.activeElement as HTMLElement)
          items[(at + (e.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length]?.focus()
        }}
        onClick={(e) => e.stopPropagation()}
        onDoubleClick={(e) => e.stopPropagation()}
      >
        <div role="menu" aria-label={t('composer.actionsFor', { name: isRoot ? '$' : row.field.name })}>
          {isContainer || isRoot ? (
            <>
              {item(t('composer.addField'), () => onAdd('field'))}
              {item(t('composer.addObject'), () => onAdd('object'))}
              {item(t('composer.addArray'), () => onAdd('array'))}
              {item(t('composer.pasteJson'), onPaste)}
            </>
          ) : null}
          {isContainer && !isRoot ? item(t(bound ? 'composer.rebind' : 'composer.bindTable'), onBind) : null}
          {isContainer && !isRoot && onGroupRows ? item(t('composer.groupRows'), onGroupRows) : null}
          {!isContainer || isRoot ? item(t('composer.lookupTable'), onBind) : null}
          {bound ? item(t('composer.unbind'), onUnbind) : null}
          {!isContainer && !isRoot ? item(t('composer.mapColumn'), onMap) : null}
          {!isContainer && embedTable && onEmbed ? item(t('composer.embedHere', { table: embedTable }), onEmbed) : null}
          {onCollapse ? item(t('composer.collapseToId'), onCollapse) : null}
          {!isRoot ? item(t('composer.rename'), onRename) : null}
          {!isRoot ? item(t('composer.delete'), onDelete, true) : null}
        </div>
      </PopoverContent>
    </Popover>
  )
}
