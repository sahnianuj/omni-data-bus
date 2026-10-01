import { Sparkles, Wand2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import type { NodeScore } from '@/features/mapping/computeStats'

/**
 * The composer's score (OC-049 AC5): the total, then each bound scope from the root down —
 * "root 8/10 · items 5/6 · items.product 3/3" — with Auto-map all.
 */
export function ScoreHeader({ byNode, onAutoMapAll, busy, onSelect, unbound = 0, onSuggest }: { byNode: NodeScore[]; onAutoMapAll: () => void; busy?: boolean; onSelect?: (nodeId: string) => void; unbound?: number; onSuggest?: () => void }) {
  const { t } = useTranslation()
  const mapped = byNode.reduce((n, s) => n + s.mapped, 0)
  const total = byNode.reduce((n, s) => n + s.total, 0)
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-border px-4 py-2 text-[13px]">
      <span className="font-medium" data-testid="score-total">
        {t('composer.score.total', { mapped, total })}
      </span>
      <ol aria-label={t('composer.score.byNode')} className="flex flex-wrap items-center gap-x-1 text-muted">
        {byNode.map((s, i) => (
          <li key={s.nodeId} className="flex items-center gap-1">
            {i > 0 ? <span aria-hidden>·</span> : null}
            <button type="button" onClick={() => onSelect?.(s.nodeId)} className="rounded px-0.5 hover:bg-surface-muted hover:text-foreground" aria-label={t('composer.score.node', { path: s.path, mapped: s.mapped, total: s.total })}>
              <span className="font-mono text-xs">{s.path}</span> {s.mapped}/{s.total}
            </button>
          </li>
        ))}
      </ol>
      {unbound ? <span className="text-xs text-muted">{t('composer.suggest.unbound', { count: unbound })}</span> : null}
      {onSuggest ? (
        <Button size="sm" variant="secondary" className="ml-auto" onClick={onSuggest}>
          <Wand2 aria-hidden />
          {t('composer.suggest.open')}
        </Button>
      ) : null}
      <Button size="sm" variant="secondary" className={onSuggest ? undefined : 'ml-auto'} loading={busy} onClick={onAutoMapAll}>
        <Sparkles aria-hidden />
        {t('composer.score.autoMapAll')}
      </Button>
    </div>
  )
}
