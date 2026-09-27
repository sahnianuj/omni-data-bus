import { AlertTriangle, ChevronRight } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Can } from '@/features/auth/Can'
import { cn } from '@/lib/utils'
import { EmbedDialog } from './EmbedDialog'
import { useWorkbench } from './store'
import type { EmbedCandidate } from './types'

/** Default one-to-many limit (mapping.DefaultEmbedLimit). */
export const DEFAULT_EMBED_LIMIT = 1000

/**
 * The source panel's Related tables (OC-021 AC1): foreign-key parents (↗, many-to-one) and
 * children (↙, one-to-many) of the mapped table, each with Embed.
 */
export function RelatedTables() {
  const { t, i18n } = useTranslation()
  const candidates = useWorkbench((s) => s.view.embedCandidates) ?? []
  const [open, setOpen] = useState(true)
  const [embedding, setEmbedding] = useState<EmbedCandidate | null>(null)
  const n = (x: number) => Math.round(x).toLocaleString(i18n.language)

  return (
    <section className="shrink-0 border-t border-border" aria-label={t('embed.related')}>
      <button type="button" className="flex h-9 w-full items-center gap-1.5 bg-surface-muted px-3 text-xs font-medium text-muted" aria-expanded={open} onClick={() => setOpen(!open)}>
        <ChevronRight className={cn('size-3.5 transition-transform', open && 'rotate-90')} aria-hidden />
        {t('embed.related')}
        <span className="ml-auto text-subtle">{candidates.length}</span>
      </button>
      {open ? (
        <ul className="max-h-48 overflow-y-auto py-1">
          {candidates.length === 0 ? <li className="px-3 py-2 text-xs text-subtle">{t('embed.none')}</li> : null}
          {candidates.map((c) => {
            const columns = c.join.map((j) => (c.kind === 'many-to-one' ? j.parent : j.child)).join(', ')
            const tooMany = c.kind === 'one-to-many' && (c.rowsPerParent ?? 0) > DEFAULT_EMBED_LIMIT
            return (
              <li key={`${c.kind}:${c.table.schema}.${c.table.name}:${c.constraint}`} className="flex items-center gap-2 px-3 py-1.5 text-[13px]">
                <div className="min-w-0 flex-1">
                  <div className="truncate">{t(c.kind === 'many-to-one' ? 'embed.parent' : 'embed.child', { table: c.table.name, columns })}</div>
                  <div className="flex items-center gap-1 text-[11px] text-subtle">
                    {c.kind === 'one-to-many' && c.rowsPerParent ? t('embed.perParent', { n: n(c.rowsPerParent) }) : c.rowEstimate != null ? t('embed.rows', { n: n(c.rowEstimate) }) : null}
                    {tooMany ? (
                      <span className="inline-flex items-center gap-0.5 text-warning" title={t('embed.tooMany', { n: n(c.rowsPerParent ?? 0), limit: DEFAULT_EMBED_LIMIT })}>
                        <AlertTriangle className="size-3" aria-label={t('embed.tooMany', { n: n(c.rowsPerParent ?? 0), limit: DEFAULT_EMBED_LIMIT })} />
                      </span>
                    ) : null}
                  </div>
                </div>
                {c.embedId ? (
                  <Badge tone="success">{t('embed.embedded')}</Badge>
                ) : (
                  <Can role="editor">
                    <Button size="sm" variant="secondary" onClick={() => setEmbedding(c)}>
                      {t('embed.embed')}
                    </Button>
                  </Can>
                )}
              </li>
            )
          })}
        </ul>
      ) : null}
      {embedding ? <EmbedDialog candidate={embedding} onClose={() => setEmbedding(null)} /> : null}
    </section>
  )
}
