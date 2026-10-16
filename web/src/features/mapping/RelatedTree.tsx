import { AlertTriangle, ChevronRight, Link2 } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Can } from '@/features/auth/Can'
import { cn } from '@/lib/utils'
import { DEFAULT_EMBED_LIMIT, EmbedDialog, type EmbedHop, type EmbedPlan } from './EmbedDialog'
import { useRelationGraph } from './relationsApi'
import { fieldsById } from './embeds'
import { useWorkbench } from './store'
import type { EmbedCandidate, JunctionCandidate, MappingSpec, RelationHop } from './types'
import { VirtualRelationshipDialog } from './VirtualRelationshipDialog'

type JoinPair = { parent: string; child: string }

/** A node of the tree: one hop, the hops reachable after it, and what the view knows about it. */
type Node = { key: string; hop: EmbedHop & { indexed?: boolean; relationshipId?: string }; children: Node[]; candidate?: EmbedCandidate }

const joinKey = (j: JoinPair[]) => j.map((p) => `${p.parent}=${p.child}`).join(',')
const hopKey = (h: { kind: string; to?: { schema: string; name: string }; table?: { schema: string; name: string }; join: JoinPair[] }) => {
  const t = h.to ?? h.table!
  return `${h.kind}|${t.schema}.${t.name}|${joinKey(h.join)}`
}
const sameJoin = (a: JoinPair[] | undefined, b: JoinPair[]) => joinKey(a ?? []) === joinKey(b)

/** The relation already embedding hop under parent ('' = the root), if any. */
function relationFor(spec: MappingSpec, parent: string, hop: EmbedHop) {
  return spec.relations.find(
    (r) => (r.parentId ?? '') === parent && r.kind === hop.kind && r.table.schema === hop.table.schema && r.table.name === hop.table.name && sameJoin(r.join, hop.join),
  )
}

const toHop = (h: RelationHop): Node['hop'] => ({ kind: h.kind, table: h.to, join: h.join, virtual: h.virtual, indexed: h.indexed, relationshipId: h.relationshipId })

/**
 * The source panel's Related tables as a tree (OC-042 AC6; OC-021 AC1 at the first level):
 * foreign-key parents (↗) and children (↙) of the mapped table and of theirs, several hops
 * deep, virtual relationships (badged), junction candidates (↔ products via order_items) and
 * index advice. Embedding a deeper row nests it under the relations already embedded on its
 * path, creating the missing ones in one step (OC-043 AC7).
 */
export function RelatedTree() {
  const { t, i18n } = useTranslation()
  const view = useWorkbench((s) => s.view)
  const spec = useWorkbench((s) => s.history.present)
  const candidates = useMemo(() => view.embedCandidates ?? [], [view.embedCandidates])
  const graph = useRelationGraph(`${view.projectId}::source`, `${view.sourceSchema}.${view.sourceTable}`)
  const [open, setOpen] = useState(true)
  // Rows the user opened or closed: an embedded row starts open (what it references can be
  // embedded next), any other starts closed.
  const [toggled, setToggled] = useState<Set<string>>(new Set())
  const [plan, setPlan] = useState<EmbedPlan | null>(null)
  const [virtualOpen, setVirtualOpen] = useState(false)
  const n = (x: number) => Math.round(x).toLocaleString(i18n.language)

  // Level 1 follows the view's candidates (their estimates and order); the graph adds deeper
  // hops, virtual relationships the view doesn't list yet, and index flags.
  const roots = useMemo<Node[]>(() => {
    const byKey = new Map<string, Node>()
    const out: Node[] = []
    for (const c of candidates) {
      const node: Node = { key: hopKey(c), hop: { kind: c.kind, table: c.table, join: c.join, virtual: !!c.virtual, relationshipId: c.virtual }, children: [], candidate: c }
      byKey.set(node.key, node)
      out.push(node)
    }
    for (const p of graph.data?.paths ?? []) {
      let level = out
      let prefix = ''
      for (const h of p.hops) {
        const key = `${prefix}/${hopKey(h)}`
        const lookupKey = prefix === '' ? hopKey(h) : key
        let node = prefix === '' ? byKey.get(lookupKey) : level.find((x) => x.key === key)
        if (!node) {
          node = { key: prefix === '' ? hopKey(h) : key, hop: toHop(h), children: [] }
          if (prefix === '') byKey.set(node.key, node)
          level.push(node)
        } else if (node.hop.indexed === undefined) node.hop.indexed = h.indexed
        level = node.children
        prefix = key
      }
    }
    return out
  }, [candidates, graph.data])
  const advice = useMemo(() => new Map((graph.data?.advice ?? []).map((a) => [`${a.table.schema}.${a.table.name}|${a.columns.join(',')}`, a.message])), [graph.data])
  const junctions = graph.data?.junctions ?? []

  // In a grouped mapping (OC-063) a relation joined on a column that isn't grouped on differs
  // per row, so it belongs in the group's rows, not on the document (OC-069).
  const group = spec.relations.find((r) => r.group)
  const groupBy = spec.root?.groupBy ?? []
  const groupName = group?.targetFieldId ? (fieldsById(spec).get(group.targetFieldId)?.name ?? '') : ''
  const base = (join: { parent?: string }[]) => (group && join.some((j) => !groupBy.includes(j.parent ?? '')) ? group.id : '')

  /** Embeds a path: the relations already embedded along it are reused; the rest are created. */
  const embedPath = (path: Node[]) => {
    let parent = base(path[0]!.hop.join)
    let label = parent ? groupName : ''
    let i = 0
    for (; i < path.length; i++) {
      const r = relationFor(spec, parent, path[i]!.hop)
      if (!r) break
      parent = r.id
      label = path[i]!.hop.table.name
    }
    setPlan({ mode: 'hops', hops: path.slice(i).map((x) => ({ kind: x.hop.kind, table: x.hop.table, join: x.hop.join, virtual: x.hop.virtual })), parentId: parent || undefined, parentLabel: label || undefined })
  }

  const embeddedPath = (path: Node[]) => {
    let parent = base(path[0]!.hop.join)
    for (const x of path) {
      const r = relationFor(spec, parent, x.hop)
      if (!r) return false
      parent = r.id
    }
    return true
  }

  const toggle = (key: string) => {
    const next = new Set(toggled)
    if (next.has(key)) next.delete(key)
    else next.add(key)
    setToggled(next)
  }

  const renderNode = (node: Node, path: Node[], depth: number) => {
    const c = node.candidate
    const h = node.hop
    const full = [...path, node]
    const columns = h.join.map((j) => (h.kind === 'many-to-one' ? j.parent : j.child)).join(', ')
    const perParent = c?.kind === 'one-to-many' ? (c.rowsPerParent ?? 0) : 0
    const tooMany = perParent > DEFAULT_EMBED_LIMIT
    const noIndex = h.kind === 'one-to-many' && h.indexed === false ? advice.get(`${h.table.schema}.${h.table.name}|${h.join.map((j) => j.child).join(',')}`) : undefined
    const embedded = embeddedPath(full)
    const isOpen = embedded !== toggled.has(node.key)
    return (
      <li key={node.key} data-testid={`related-${full.map((x) => x.hop.table.name).join('/')}`}>
        <div className="flex items-center gap-2 py-1.5 pr-3 text-[13px]" style={{ paddingLeft: 12 + depth * 14 }}>
          {node.children.length > 0 ? (
            <button type="button" className="text-subtle" aria-expanded={isOpen} aria-label={t(isOpen ? 'embed.collapseHop' : 'embed.expandHop', { table: h.table.name })} onClick={() => toggle(node.key)}>
              <ChevronRight className={cn('size-3.5 transition-transform', isOpen && 'rotate-90')} />
            </button>
          ) : (
            <span className="w-3.5" />
          )}
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-1 truncate">
              {t(h.kind === 'many-to-one' ? 'embed.parent' : 'embed.child', { table: h.table.name, columns })}
              {h.virtual ? <Badge tone="primary">{t('embed.virtual')}</Badge> : null}
            </div>
            <div className="flex items-center gap-1 text-[11px] text-subtle">
              {c?.kind === 'one-to-many' && c.rowsPerParent ? t('embed.perParent', { n: n(c.rowsPerParent) }) : c?.rowEstimate != null ? t('embed.rows', { n: n(c.rowEstimate) }) : null}
              {tooMany ? (
                <span className="inline-flex items-center gap-0.5 text-warning" title={t('embed.tooMany', { n: n(perParent), limit: DEFAULT_EMBED_LIMIT })}>
                  <AlertTriangle className="size-3" aria-label={t('embed.tooMany', { n: n(perParent), limit: DEFAULT_EMBED_LIMIT })} />
                </span>
              ) : null}
              {noIndex ? (
                <span className="inline-flex items-center gap-0.5 text-warning" title={noIndex}>
                  <AlertTriangle className="size-3" aria-label={noIndex} />
                  {t('embed.noIndex')}
                </span>
              ) : null}
            </div>
          </div>
          {embedded ? (
            <Badge tone="success">{t('embed.embedded')}</Badge>
          ) : (
            <Can role="editor">
              <Button size="sm" variant="secondary" onClick={() => embedPath(full)}>
                {t('embed.embed')}
              </Button>
            </Can>
          )}
        </div>
        {isOpen && node.children.length > 0 ? <ul>{node.children.map((k) => renderNode(k, full, depth + 1))}</ul> : null}
      </li>
    )
  }

  const junctionEmbedded = (j: JunctionCandidate) =>
    spec.relations.some((r) => (r.parentId ?? '') === base(j.parentJoin) && r.kind === 'many-to-many' && r.table.name === j.table.name && r.via?.table.name === j.via.name)

  return (
    <section className="shrink-0 border-t border-border" aria-label={t('embed.related')}>
      <div className="flex h-9 items-center bg-surface-muted pr-2 text-xs font-medium text-muted">
        <button type="button" className="flex h-full flex-1 items-center gap-1.5 px-3" aria-expanded={open} onClick={() => setOpen(!open)}>
          <ChevronRight className={cn('size-3.5 transition-transform', open && 'rotate-90')} aria-hidden />
          {t('embed.related')}
          <span className="ml-auto text-subtle">{roots.length}</span>
        </button>
        <Can role="editor">
          <button type="button" className="ml-2 inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-muted hover:bg-surface hover:text-foreground" onClick={() => setVirtualOpen(true)}>
            <Link2 className="size-3.5" aria-hidden />
            {t('virtual.open')}
          </button>
        </Can>
      </div>
      {open ? (
        <div className="max-h-72 overflow-y-auto py-1">
          <ul>
            {roots.length === 0 ? <li className="px-3 py-2 text-xs text-subtle">{t('embed.none')}</li> : null}
            {roots.map((r) => renderNode(r, [], 0))}
          </ul>
          {junctions.length > 0 ? (
            <>
              <div className="px-3 pb-1 pt-2 text-[11px] font-medium uppercase tracking-wide text-subtle">{t('embed.junctions')}</div>
              <ul aria-label={t('embed.junctions')}>
                {junctions.map((j) => (
                  <li key={`${j.via.name}-${j.table.name}-${joinKey(j.childJoin.map((c) => ({ parent: c.via, child: c.child ?? '' })))}`} data-testid={`junction-${j.via.name}-${j.table.name}`} className="flex items-center gap-2 py-1.5 pl-[26px] pr-3 text-[13px]">
                    <div className="min-w-0 flex-1">
                      <div className="truncate">
                        {t('embed.junction', { table: j.table.name, via: j.via.name })}
                        {j.payloadColumns.length > 0 ? <span className="text-subtle"> ({j.payloadColumns.join(', ')})</span> : null}
                      </div>
                      <div className="text-[11px] text-subtle">{t(`embed.confidence.${j.confidence}`)}</div>
                    </div>
                    {junctionEmbedded(j) ? (
                      <Badge tone="success">{t('embed.embedded')}</Badge>
                    ) : (
                      <Can role="editor">
                        <Button size="sm" variant="secondary" onClick={() => setPlan({ mode: 'junction', junction: j, ...(base(j.parentJoin) ? { parentId: base(j.parentJoin), parentLabel: groupName } : {}) })}>
                          {t('embed.embedArray', { table: j.table.name })}
                        </Button>
                      </Can>
                    )}
                  </li>
                ))}
              </ul>
            </>
          ) : null}
        </div>
      ) : null}
      {plan ? <EmbedDialog plan={plan} onClose={() => setPlan(null)} /> : null}
      {virtualOpen ? <VirtualRelationshipDialog onClose={() => setVirtualOpen(false)} /> : null}
    </section>
  )
}
