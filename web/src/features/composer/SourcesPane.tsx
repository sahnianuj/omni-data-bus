import { ArrowDownLeft, ArrowLeftRight, ArrowUpRight, Circle, Plus, Rows3 } from 'lucide-react'
import { useMemo, useState, type DragEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Can } from '@/features/auth/Can'
import { useSchema } from '@/features/discovery/api'
import { useRelationGraph } from '@/features/mapping/relationsApi'
import { useWorkbench } from '@/features/mapping/store'
import type { RelationHop } from '@/features/mapping/types'
import { VirtualRelationshipDialog } from '@/features/mapping/VirtualRelationshipDialog'
import { humanizeCount } from '@/lib/format'
import { cn } from '@/lib/utils'
import { edges, scopeTable } from './bindPrefill'
import { nodeOf, type TableRef } from './composerOps'
import { DND_GROUP, DND_TABLE } from './DocumentTree'
import { boundRelations, scope } from './scope'

const key = (t: TableRef) => `${t.schema}.${t.name}`

type Reach = 'scope' | 'reachable' | 'unreachable'

type Item = { key: string; table: TableRef; depth: number; icon: 'parent' | 'child' | 'junction'; via?: TableRef; virtual?: boolean }

/**
 * The Sources pane (OC-048): the driving table and its relationship tree (OC-042) — foreign keys
 * both ways, virtual relationships and junctions — then every other table. While a node is
 * selected, tables in its scope are solid, tables it can reach outlined, the rest dimmed. Drag a
 * table onto a node to bind it; Enter on a table binds it to the selected node.
 */
export function SourcesPane({ onBind, onGroupRows }: { onBind: (nodeId: string, table: TableRef, lookup: boolean) => void; onGroupRows?: (nodeId: string) => void }) {
  const { t } = useTranslation()
  const view = useWorkbench((s) => s.view)
  const spec = useWorkbench((s) => s.history.present)
  const selection = useWorkbench((s) => s.selection)
  const conn = `${view.projectId}::source`
  const own = useMemo(() => ({ schema: view.sourceSchema, name: view.sourceTable }), [view.sourceSchema, view.sourceTable])
  const driving = spec.root?.table ?? own
  const graph = useRelationGraph(conn, key(driving))
  const snap = useSchema(conn)
  const [defining, setDefining] = useState(false)
  const nodeId = selection?.kind === 'field' ? selection.id : spec.docSchema.root.id
  const node = nodeOf(spec, nodeId)
  const bindable = !!node && (node.type === 'object' || node.type === 'array') && nodeId !== spec.docSchema.root.id

  const estimates = useMemo(() => {
    const out = new Map<string, number>()
    for (const s of snap.data?.schemas ?? []) for (const tb of s.tables) if (tb.rowEstimate != null) out.set(`${s.name}.${tb.name}`, tb.rowEstimate)
    return out
  }, [snap.data])

  // The tree from the driving table: each hop once, nested under the hop before it.
  const items = useMemo(() => {
    const out: Item[] = []
    const seen = new Set<string>()
    for (const p of graph.data?.paths ?? []) {
      let prefix = ''
      p.hops.forEach((h: RelationHop, depth) => {
        prefix += `/${key(h.to)}|${h.join.map((j) => j.parent + '=' + j.child).join(',')}`
        if (seen.has(prefix)) return
        seen.add(prefix)
        out.push({ key: prefix, table: h.to, depth: depth + 1, icon: h.kind === 'many-to-one' ? 'parent' : 'child', virtual: h.virtual })
      })
    }
    for (const j of graph.data?.junctions ?? []) out.push({ key: `m2m/${key(j.table)}/${key(j.via)}`, table: j.table, depth: 1, icon: 'junction', via: j.via })
    return out
  }, [graph.data])

  const others = useMemo(() => {
    const inGraph = new Set([key(driving), ...items.map((i) => key(i.table))])
    return (snap.data?.schemas ?? []).flatMap((s) => s.tables.filter((tb) => tb.readable).map((tb) => ({ schema: s.name, name: tb.name }))).filter((tb) => !inGraph.has(key(tb)))
  }, [snap.data, items, driving])

  // Reach from the selected node: its scope (and its own binding), then one hop further.
  const reach = useMemo(() => {
    const inScope = new Set((scope(spec, nodeId) ?? [{ table: driving }]).map((e) => key(e.table)))
    const self = boundRelations(spec).get(nodeId)
    if (self) inScope.add(key(self.table))
    if (nodeId === spec.docSchema.root.id) inScope.add(key(driving))
    const from = scopeTable(spec, nodeId, own)
    const next = new Set<string>()
    for (const h of edges(graph.data)) if (key(h.from) === key(from) || inScope.has(key(h.from))) next.add(key(h.to))
    if (graph.data && key(graph.data.root) === key(from)) for (const j of graph.data.junctions) next.add(key(j.table))
    return (tb: TableRef): Reach => (inScope.has(key(tb)) ? 'scope' : next.has(key(tb)) ? 'reachable' : 'unreachable')
  }, [spec, nodeId, driving, own, graph.data])

  const row = (it: { key: string; table: TableRef; depth: number; icon?: Item['icon'] | 'driving'; via?: TableRef; virtual?: boolean }) => {
    const r = it.icon === 'driving' ? 'scope' : reach(it.table)
    const Icon = it.icon === 'parent' ? ArrowUpRight : it.icon === 'child' ? ArrowDownLeft : it.icon === 'junction' ? ArrowLeftRight : Circle
    const est = estimates.get(key(it.table))
    const target = node?.name ?? '$'
    const onDragStart = (e: DragEvent) => {
      e.dataTransfer.setData(DND_TABLE, JSON.stringify(it.table))
      e.dataTransfer.effectAllowed = 'link'
    }
    return (
      <li key={it.key} style={{ paddingLeft: 8 + (it.depth - 1) * 14 }}>
        <button
          type="button"
          draggable
          onDragStart={onDragStart}
          onClick={() => onBind(nodeId, it.table, !bindable)}
          aria-label={t(bindable ? 'composer.sources.bindTo' : 'composer.sources.lookupAt', { table: it.table.name, node: target }) + ', ' + t(`composer.sources.reach.${r}`)}
          data-reach={r}
          className={cn(
            'flex h-7 w-full items-center gap-1.5 rounded px-1.5 text-left text-[13px] hover:bg-surface-muted',
            // Dimmed, but still readable (WCAG AA): unrelated tables can be bound after defining a relationship.
            r === 'scope' && 'font-medium text-foreground',
            r === 'reachable' && 'text-foreground outline outline-1 -outline-offset-1 outline-border-strong',
            r === 'unreachable' && 'text-muted [&>svg]:opacity-40',
          )}
        >
          <Icon className={cn('size-3.5 shrink-0', it.icon === 'driving' ? 'fill-primary text-primary' : 'text-subtle')} aria-hidden />
          <span className="truncate">{it.table.name}</span>
          {it.icon === 'driving' ? <span className="text-xs text-muted">{t('composer.sources.driving')}</span> : null}
          {it.via ? <span className="truncate text-xs text-muted">{t('composer.sources.via', { via: it.via.name })}</span> : null}
          {it.virtual ? <Badge tone="primary">{t('embed.virtual')}</Badge> : null}
          {est != null ? <span className="ml-auto font-mono text-[11px] text-muted">{humanizeCount(est)}</span> : null}
        </button>
      </li>
    )
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <h2 className="px-3 pt-3 text-xs font-semibold uppercase tracking-wide text-muted">{t('composer.sources.title')}</h2>
      <p className="px-3 pb-2 text-xs text-muted">{t(bindable ? 'composer.sources.hintBind' : 'composer.sources.hint')}</p>
      <ul aria-label={t('composer.sources.title')} className="flex flex-col gap-0.5 overflow-y-auto px-1.5 pb-2">
        {row({ key: 'driving', table: driving, depth: 1, icon: 'driving' })}
        {spec.root?.groupBy?.length && onGroupRows ? (
          <li style={{ paddingLeft: 22 }}>
            <button
              type="button"
              draggable
              onDragStart={(e) => {
                e.dataTransfer.setData(DND_GROUP, '1')
                e.dataTransfer.effectAllowed = 'link'
              }}
              disabled={!bindable}
              onClick={() => onGroupRows(nodeId)}
              aria-label={t('composer.sources.groupRowsTo', { node: node?.name ?? '$', table: driving.name })}
              className="flex h-7 w-full items-center gap-1.5 rounded px-1.5 text-left text-[13px] font-medium hover:bg-surface-muted disabled:cursor-default"
            >
              <Rows3 className="size-3.5 shrink-0 text-primary" aria-hidden />
              <span className="truncate">{t('composer.sources.groupRows')}</span>
              <span className="truncate text-xs text-muted">{driving.name}</span>
            </button>
          </li>
        ) : null}
        {graph.isLoading ? <li className="px-3 py-1 text-xs text-muted">{t('common.loading')}</li> : null}
        {items.map((it) => row({ ...it, depth: it.depth + 1 }))}
        {others.length ? (
          <li className="px-2 pt-3 text-[11px] font-semibold uppercase tracking-wide text-muted" aria-hidden>
            {t('composer.sources.others')}
          </li>
        ) : null}
        {others.map((tb) => row({ key: `other/${key(tb)}`, table: tb, depth: 1 }))}
      </ul>
      <Can role="editor">
        <div className="border-t border-border p-2">
          <Button variant="ghost" size="sm" onClick={() => setDefining(true)}>
            <Plus aria-hidden />
            {t('composer.sources.virtual')}
          </Button>
        </div>
      </Can>
      {defining ? <VirtualRelationshipDialog initialFrom={key(driving)} onClose={() => setDefining(false)} /> : null}
    </div>
  )
}
