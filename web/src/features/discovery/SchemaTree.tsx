import { useVirtualizer } from '@tanstack/react-virtual'
import { ArrowUpRight, ChevronRight, Diamond, Eye, KeyRound, Layers, LayoutGrid, Lock, Network, Table2 } from 'lucide-react'
import { useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { humanizeCount } from '@/lib/format'
import { cn } from '@/lib/utils'
import type { TableKind } from './api'
import { expandable, parentId, type Range, type TreeNode } from './tree'

const ROW_HEIGHT = 30

const kindIcon: Record<TableKind, ReactNode> = {
  table: <Table2 aria-hidden />,
  partitioned_table: <LayoutGrid aria-hidden />,
  view: <Eye aria-hidden />,
  materialized_view: <Layers aria-hidden />,
  foreign_table: <Network aria-hidden />,
}

/** Renders label text with the matched ranges highlighted. */
export function Highlight({ text, ranges }: { text: string; ranges?: Range[] }) {
  if (!ranges?.length) return <>{text}</>
  const parts: ReactNode[] = []
  let at = 0
  for (const [a, b] of ranges) {
    if (a < at) continue
    if (a > at) parts.push(text.slice(at, a))
    parts.push(
      <mark key={a} className="rounded-sm bg-warning-soft text-foreground">
        {text.slice(a, b + 1)}
      </mark>,
    )
    at = b + 1
  }
  parts.push(text.slice(at))
  return <>{parts}</>
}

type Props = {
  nodes: TreeNode[]
  expanded: ReadonlySet<string>
  setOpen: (id: string, open: boolean) => void
  highlights: Map<string, Range[]>
  selected?: string
  onSelect: (schema: string, table: string) => void
  label: string
}

/**
 * The schema tree (OC-011): virtualized (thousands of nodes render only the visible rows) and
 * keyboard-accessible following the ARIA tree pattern. Focus stays on the tree; the active row is
 * announced through aria-activedescendant.
 */
export function SchemaTree({ nodes, expanded, setOpen, highlights, selected, onSelect, label }: Props) {
  const { t } = useTranslation()
  const scrollRef = useRef<HTMLDivElement>(null)
  const [active, setActive] = useState<string | undefined>(undefined)
  const typeahead = useRef({ text: '', at: 0 })
  // TanStack Virtual returns fresh functions each render, so the compiler skips this component;
  // that's expected and harmless here (nothing memoized depends on the virtualizer).
  // eslint-disable-next-line react-hooks/incompatible-library
  const virtualizer = useVirtualizer({
    count: nodes.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ROW_HEIGHT,
    overscan: 12,
    initialRect: { width: 280, height: 600 },
  })

  const activeIndex = Math.max(0, nodes.findIndex((n) => n.id === (active ?? selected)))
  const current = nodes[activeIndex] as TreeNode | undefined
  const domId = (i: number) => `schema-tree-node-${i}`

  const moveTo = (i: number) => {
    const n = nodes[Math.min(Math.max(i, 0), nodes.length - 1)]
    if (!n) return
    setActive(n.id)
    virtualizer.scrollToIndex(nodes.indexOf(n), { align: 'auto' })
  }

  const activate = (n: TreeNode) => {
    if (n.kind === 'schema') setOpen(n.id, !expanded.has(n.id))
    else if (n.kind === 'table') onSelect(n.schema, n.name)
    else onSelect(n.schema, n.table)
  }

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (!current) return
    const open = expanded.has(current.id)
    switch (e.key) {
      case 'ArrowDown':
        moveTo(activeIndex + 1)
        break
      case 'ArrowUp':
        moveTo(activeIndex - 1)
        break
      case 'Home':
        moveTo(0)
        break
      case 'End':
        moveTo(nodes.length - 1)
        break
      case 'ArrowRight':
        if (!expandable(current)) return
        if (open) moveTo(activeIndex + 1)
        else setOpen(current.id, true)
        break
      case 'ArrowLeft': {
        if (expandable(current) && open) {
          setOpen(current.id, false)
          break
        }
        const parent = parentId(current)
        if (parent) moveTo(nodes.findIndex((n) => n.id === parent))
        break
      }
      case 'Enter':
      case ' ':
        activate(current)
        break
      default:
        if (e.key.length !== 1 || e.metaKey || e.ctrlKey || e.altKey) return
      {
        // Type-ahead: keys typed within 600 ms build a prefix; a single key jumps to the next match.
        const text = (Date.now() - typeahead.current.at < 600 ? typeahead.current.text : '') + e.key.toLowerCase()
        typeahead.current = { text, at: Date.now() }
        const start = text.length > 1 ? activeIndex : activeIndex + 1
        for (let k = 0; k < nodes.length; k++) {
          const i = (start + k) % nodes.length
          if (nodes[i]?.name.toLowerCase().startsWith(text)) {
            moveTo(i)
            break
          }
        }
        break
      }
    }
    e.preventDefault()
  }

  return (
    <div
      ref={scrollRef}
      role="tree"
      aria-label={label}
      tabIndex={0}
      aria-activedescendant={current ? domId(activeIndex) : undefined}
      onKeyDown={onKeyDown}
      className="relative min-h-0 flex-1 overflow-y-auto py-1 outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
    >
      <div style={{ height: virtualizer.getTotalSize(), position: 'relative' }}>
        {virtualizer.getVirtualItems().map((v) => {
          const n = nodes[v.index]
          if (!n) return null
          const isActive = v.index === activeIndex
          const isSelected = n.kind === 'table' && n.id === selected
          const canOpen = expandable(n)
          const open = expanded.has(n.id)
          return (
            <div
              key={n.id}
              id={domId(v.index)}
              role="treeitem"
              aria-level={n.level}
              aria-expanded={canOpen ? open : undefined}
              aria-selected={n.kind === 'table' ? isSelected : undefined}
              data-active={isActive || undefined}
              onClick={() => {
                setActive(n.id)
                activate(n)
              }}
              className={cn(
                'absolute left-0 right-0 flex cursor-default select-none items-center gap-1.5 pr-3 text-[13px] [&_svg]:size-3.5 [&_svg]:shrink-0',
                isSelected ? 'bg-primary-soft text-primary-soft-foreground' : 'hover:bg-surface-muted',
                isActive && 'ring-1 ring-inset ring-ring',
              )}
              style={{ top: v.start, height: ROW_HEIGHT, paddingLeft: 8 + (n.level - 1) * 16 }}
            >
              {canOpen ? (
                <button
                  type="button"
                  tabIndex={-1}
                  aria-hidden
                  onClick={(e) => {
                    e.stopPropagation()
                    setActive(n.id)
                    setOpen(n.id, !open)
                  }}
                  className="flex size-4 items-center justify-center rounded text-subtle hover:text-foreground"
                >
                  <ChevronRight className={cn('transition-transform', open && 'rotate-90')} />
                </button>
              ) : (
                <span className="size-4" />
              )}
              {n.kind === 'schema' ? (
                <>
                  <span className="truncate font-medium">{n.name}</span>
                  <span className="text-xs text-subtle">({n.count})</span>
                </>
              ) : n.kind === 'table' ? (
                <>
                  <span className="text-subtle">{kindIcon[n.table.kind]}</span>
                  <span className="truncate">
                    <Highlight text={n.name} ranges={highlights.get(n.id)} />
                  </span>
                  {!n.table.readable ? <Lock className="text-warning" aria-label={t('explorer.noAccess')} /> : null}
                  <span className="ml-auto pl-2 font-mono text-[11px] text-subtle" title={n.table.statsStale ? t('explorer.statsStale') : undefined}>
                    {n.table.rowEstimate != null ? `~${humanizeCount(n.table.rowEstimate)}` : n.table.kind.includes('view') ? '' : '—'}
                  </span>
                </>
              ) : (
                <>
                  <span className="flex w-3.5 justify-center text-subtle">
                    {n.column.isPrimaryKey ? (
                      <KeyRound className="text-warning" aria-label={t('explorer.pk')} />
                    ) : n.fk ? (
                      <ArrowUpRight className="text-primary" aria-label={t('explorer.fk')} />
                    ) : n.column.isUnique ? (
                      <Diamond aria-label={t('explorer.unique')} />
                    ) : null}
                  </span>
                  <span className="truncate">
                    <Highlight text={n.name} ranges={highlights.get(n.id)} />
                  </span>
                  <span className="ml-auto truncate pl-2 font-mono text-[11px] text-subtle">{n.column.udtName}</span>
                </>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}
