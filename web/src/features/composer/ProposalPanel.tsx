import { Undo2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Can } from '@/features/auth/Can'
import type { ProposalItem } from '@/features/mapping/api'
import { setNotPopulated } from '@/features/mapping/mappingOps'
import { useWorkbench } from '@/features/mapping/store'
import { VirtualRelationshipDialog } from '@/features/mapping/VirtualRelationshipDialog'
import { ApiError } from '@/lib/api/client'
import { cn } from '@/lib/utils'
import { acceptSuggestion, setExpression, unbind } from './composerOps'
import { useReview } from './ReviewBanner'
import { edited, removeItem } from './review'
import { scope } from './scope'

type Props = {
  fieldId: string
  /** Maps a column of the field's scope onto it (the composer's drop). */
  onUseColumn: (fieldId: string, c: { relation: string; column: string }) => void
  /** Opens the import on a node (UNTYPED_ITEMS: the items' designs). */
  onImportItems: (nodeId: string) => void
}

const bandCls: Record<string, string> = { high: 'text-success', medium: 'text-warning', low: 'text-danger' }

/**
 * The Inspector's view of the selected row's proposal (OC-168): what was proposed and why, its
 * confidence, the alternatives, and for what nothing fills a reason with its fix (OC-169).
 */
export function ProposalPanel({ fieldId, onUseColumn, onImportItems }: Props) {
  const { t } = useTranslation()
  const review = useWorkbench((s) => s.review)
  const spec = useWorkbench((s) => s.history.present)
  const apply = useWorkbench((s) => s.apply)
  const { again } = useReview()
  const [virtual, setVirtual] = useState<{ from: string; to: string } | null>(null)
  const it: ProposalItem | undefined = review?.proposal.items.find((x) => x.fieldId === fieldId)
  if (!review || !it) return null
  const isEdited = edited(spec, review, it)
  const recompute = async () => {
    try {
      await again(fieldId)
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : String(e))
    }
  }
  // A candidate "table.column" this field's scope can read.
  const scopeColumn = (cand: string) => {
    const [table, column] = cand.split('.')
    const entry = (scope(spec, fieldId) ?? []).find((e) => e.table.name === table)
    return entry && column ? { relation: entry.relationId, column } : undefined
  }
  const scopeTable = () => {
    const entries = scope(spec, fieldId) ?? []
    const e = entries.at(-1)
    return e ? `${e.table.schema}.${e.table.name}` : ''
  }

  return (
    <section aria-label={t('propose.panel')} className="flex flex-col gap-2 border-b border-border bg-primary-soft/40 p-3 text-[13px]">
      <div className="flex items-center gap-2">
        <h3 className="text-xs font-semibold uppercase tracking-wide text-muted">{t('propose.panel')}</h3>
        {isEdited ? <span className="text-xs text-primary">{t('propose.chip.edited')}</span> : it.band ? <span className={cn('text-xs font-medium', bandCls[it.band])}>{t(`propose.chip.${it.band}`)}</span> : null}
        <span className="ml-auto font-mono text-[11px] text-muted">{Math.round(it.score * 100)}%</span>
      </div>
      <p className="font-mono text-xs">{it.summary}</p>
      {it.code ? <p className="text-xs font-medium text-danger">{t(`propose.code.${it.code}`)}</p> : null}
      <ul className="flex list-disc flex-col gap-0.5 pl-4 text-xs text-muted">
        {it.reasons.map((r) => (
          <li key={r}>{r}</li>
        ))}
      </ul>
      <Can role="editor">
        <div className="flex flex-wrap gap-1.5">
          {it.status === 'unresolved' || it.code === 'TYPE_MISMATCH'
            ? (it.candidates ?? []).map((c) => {
                const col = it.kind === 'field' ? scopeColumn(c) : undefined
                return col ? (
                  <Button key={c} size="sm" variant="secondary" onClick={() => onUseColumn(fieldId, col)}>
                    {t('propose.use', { what: c })}
                  </Button>
                ) : null
              })
            : null}
          {it.code === 'REFERENCE_TEMPLATE' && it.fix && isEdited ? (
            <Button size="sm" variant="secondary" onClick={() => apply((s) => setExpression(s, fieldId, it.fix!))}>
              {t('propose.useExpression')}
            </Button>
          ) : null}
          {it.code === 'NO_PATH' && it.kind === 'binding' ? (
            <Button size="sm" variant="secondary" onClick={() => setVirtual({ from: scopeTable(), to: it.candidates?.[0] ?? '' })}>
              {t('propose.addVirtual')}
            </Button>
          ) : null}
          {it.code === 'UNTYPED_ITEMS' ? (
            <Button size="sm" variant="secondary" onClick={() => onImportItems(fieldId)}>
              {t('propose.importItems')}
            </Button>
          ) : null}
          {it.kind === 'binding'
            ? (it.alternatives ?? []).map((alt, i) =>
                alt.table ? (
                  <Button
                    key={i}
                    size="sm"
                    variant="secondary"
                    title={alt.reasons.join('\n')}
                    onClick={() => {
                      apply((s) => acceptSuggestion(unbind(s, fieldId), fieldId, alt as never))
                      void recompute()
                    }}
                  >
                    {t('propose.useTable', { table: alt.table.name })}
                  </Button>
                ) : null,
              )
            : null}
          {it.kind === 'field' && it.status !== 'notPopulated' ? (
            <Button size="sm" variant="ghost" onClick={() => apply((s) => setNotPopulated(s, fieldId, true))}>
              {t('propose.markNotPopulated')}
            </Button>
          ) : null}
          {it.status !== 'unresolved' && it.status !== 'alreadyMapped' && !isEdited ? (
            <Button size="sm" variant="ghost" onClick={() => apply((s) => removeItem(s, it))}>
              <Undo2 aria-hidden />
              {t('propose.takeBack')}
            </Button>
          ) : null}
        </div>
      </Can>
      {virtual ? <VirtualRelationshipDialog initialFrom={virtual.from} initialTo={virtual.to} onClose={() => setVirtual(null)} /> : null}
    </section>
  )
}
