import { Check, ChevronRight } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Can } from '@/features/auth/Can'
import { cardinalityChip } from '@/features/mapping/relatedGroups'
import { useTablesHops } from '@/features/mapping/relationsApi'
import { useWorkbench } from '@/features/mapping/store'
import type { JunctionCandidate } from '@/features/mapping/types'
import { cn } from '@/lib/utils'
import { availableHops, documentSources, flatten, type AvailableHop, type Instance } from './documentSources'

const key = (t: { schema: string; name: string }) => `${t.schema}.${t.name}`
const isJunction = (h: AvailableHop | JunctionCandidate): h is JunctionCandidate => 'via' in h && !('from' in h)

type Props = {
  /** Embeds a hop into an instance's object: an object (many-to-one, one-to-one) or an array. */
  onEmbed: (inst: Instance, hop: AvailableHop | JunctionCandidate) => void
  /** Looks a many-to-one hop up in an instance's scope (values only, no nested object). */
  onLookup: (inst: Instance, hop: AvailableHop) => void
}

/**
 * "In this document" (OC-142): what the mapping already reads, one row per relation instance
 * nested like the document, and under each, its table's relationships not embedded yet,
 * grouped as References and Referenced by with their cardinality (OC-157), with Embed and Look
 * up. At the depth limit the actions are disabled and say why.
 */
export function InThisDocument({ onEmbed, onLookup }: Props) {
  const { t } = useTranslation()
  const view = useWorkbench((s) => s.view)
  const spec = useWorkbench((s) => s.history.present)
  const select = useWorkbench((s) => s.select)
  const own = { schema: view.sourceSchema, name: view.sourceTable }
  const root = documentSources(spec, spec.root?.table ?? own)
  const all = flatten(root)
  const { hopsOf, junctionsOf, maxRelationDepth } = useTablesHops(`${view.projectId}::source`, all.map((i) => key(i.table)))
  const [closed, setClosed] = useState<Set<string>>(new Set())

  const hopRow = (inst: Instance, h: AvailableHop | JunctionCandidate, depth: number) => {
    const junction = isJunction(h)
    const table = junction ? h.table : h.to
    const card = junction ? 'many-to-many' : h.cardinality
    const blocked = maxRelationDepth != null && inst.depth >= maxRelationDepth ? t('embed.depthLimit', { depth: inst.depth, max: maxRelationDepth }) : undefined
    const label = junction ? t('embed.junction', { table: table.name, via: h.via.name }) : t(h.direction === 'references' ? 'embed.parent' : 'embed.child', { table: table.name, columns: h.join.map((j) => (h.direction === 'references' ? j.parent : j.child)).join(', ') })
    return (
      <li key={`${inst.key}/${junction ? `m2m/${h.via.name}` : ''}${key(table)}/${junction ? '' : h.join.map((j) => j.parent + '=' + j.child).join(',')}`} data-testid={`doc-hop-${inst.key}-${table.name}`} className="flex items-center gap-1.5 py-0.5 pr-1 text-[13px]" style={{ paddingLeft: 8 + depth * 14 }}>
        <span className="min-w-0 flex-1 truncate">{label}</span>
        <span className="rounded border border-border px-1 font-mono text-[10px] text-muted" aria-label={t(`embed.card.${cardinalityChip[card].key}`)}>
          {cardinalityChip[card].text}
        </span>
        {!junction && h.virtual ? <Badge tone="primary">{t('embed.virtual')}</Badge> : null}
        <Can role="editor">
          <span className="flex shrink-0 items-center gap-1">
          <Button size="sm" variant="secondary" className="h-6 px-2 text-xs" disabled={!!blocked || !inst.containerId} title={blocked} aria-label={t('composer.doc.embed', { table: table.name, into: inst.path || inst.table.name })} onClick={() => onEmbed(inst, h)}>
            {t('embed.embed')}
          </Button>
          {!junction && h.kind === 'many-to-one' ? (
            <Button size="sm" variant="ghost" className="h-6 px-2 text-xs" disabled={!!blocked} title={blocked} aria-label={t('composer.doc.lookup', { table: table.name, into: inst.path || inst.table.name })} onClick={() => onLookup(inst, h)}>
              {t('composer.doc.lookupShort')}
            </Button>
          ) : null}
          </span>
        </Can>
      </li>
    )
  }

  const instanceRows = (inst: Instance, depth: number): React.ReactNode => {
    const open = !closed.has(inst.key)
    const avail = inst.lookup ? undefined : availableHops(spec, inst, hopsOf, junctionsOf)
    const has = inst.children.length > 0 || (avail && avail.references.length + avail.referencedBy.length > 0)
    return (
      <li key={inst.key}>
        <div className="flex items-center gap-1.5 py-0.5 pr-1 text-[13px]" style={{ paddingLeft: 8 + depth * 14 }}>
          {has ? (
            <button
              type="button"
              aria-expanded={open}
              aria-label={t(open ? 'embed.collapseHop' : 'embed.expandHop', { table: inst.table.name })}
              onClick={() => setClosed((c) => (c.has(inst.key) ? new Set([...c].filter((k) => k !== inst.key)) : new Set([...c, inst.key])))}
              className="text-subtle"
            >
              <ChevronRight className={cn('size-3.5 transition-transform', open && 'rotate-90')} />
            </button>
          ) : (
            <span className="w-3.5" />
          )}
          {inst.relationId ? <Check className="size-3.5 shrink-0 text-success" aria-hidden /> : null}
          <span className="truncate font-medium">{inst.table.name}</span>
          {inst.path ? (
            <button type="button" className="truncate font-mono text-xs text-muted hover:underline" onClick={() => select({ kind: 'field', id: spec.relations.find((r) => r.id === inst.relationId)?.targetFieldId ?? '' })}>
              → {inst.path}
            </button>
          ) : inst.lookup ? (
            <span className="text-xs text-muted">{t('composer.doc.lookupTag')}</span>
          ) : !inst.relationId ? (
            <span className="text-xs text-muted">{t('composer.sources.driving')}</span>
          ) : null}
        </div>
        {open ? (
          <ul>
            {inst.children.map((c) => instanceRows(c, depth + 1))}
            {avail
              ? (['references', 'referencedBy'] as const).map((dir) =>
                  avail[dir].length === 0 ? null : (
                    <li key={dir}>
                      <div role="group" aria-label={t(`embed.${dir}Of`, { table: inst.table.name })}>
                        <div className="py-0.5 text-[11px] text-subtle" style={{ paddingLeft: 8 + (depth + 1) * 14 + 18 }} aria-hidden>
                          {t(`embed.${dir}`)}
                        </div>
                        <ul>{avail[dir].map((h) => hopRow(inst, h, depth + 1))}</ul>
                      </div>
                    </li>
                  ),
                )
              : null}
          </ul>
        ) : null}
      </li>
    )
  }

  return (
    <section aria-label={t('composer.doc.title')} className="border-b border-border pb-2">
      <h3 className="px-3 pt-2 text-[11px] font-semibold uppercase tracking-wide text-muted">{t('composer.doc.title')}</h3>
      <ul className="px-1.5">{instanceRows(root, 0)}</ul>
    </section>
  )
}
