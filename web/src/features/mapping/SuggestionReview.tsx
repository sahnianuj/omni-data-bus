import { Check, PartyPopper, X } from 'lucide-react'
import { useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { pathsById } from '@/features/docschema/docschemaOps'
import { acceptAll, acceptSuggestion, rejectSuggestion } from './mappingOps'
import { useWorkbench } from './store'

/** Pending suggestions sorted by confidence, with accept/reject (OC-018 AC4). */
export function SuggestionReview({ onMapRemaining }: { onMapRemaining: () => void }) {
  const { t } = useTranslation()
  const spec = useWorkbench((s) => s.history.present)
  const explain = useWorkbench((s) => s.view.explain)
  const apply = useWorkbench((s) => s.apply)
  const undo = useWorkbench((s) => s.undo)
  const unmapped = useWorkbench((s) => s.stats().unmappedSource)
  const [active, setActive] = useState(0)
  const listRef = useRef<HTMLUListElement>(null)
  const paths = useMemo(() => pathsById(spec.docSchema), [spec.docSchema])
  const pending = useMemo(
    () => spec.fieldMappings.filter((m) => m.state === 'pending').sort((a, b) => (b.confidence ?? 0) - (a.confidence ?? 0)),
    [spec.fieldMappings],
  )

  const bulk = (min: number) => {
    const n = pending.filter((m) => (m.confidence ?? 0) >= min).length
    if (!n) return
    apply((s) => acceptAll(s, min))
    toast.success(t('mapping.acceptedCount', { count: n }), { action: { label: t('mapping.undo'), onClick: undo } })
  }

  if (pending.length === 0) {
    return (
      <div className="flex flex-col items-center gap-3 px-4 py-10 text-center">
        <PartyPopper className="size-7 text-success" aria-hidden />
        <p className="text-sm font-medium">{t('mapping.allReviewed')}</p>
        {unmapped > 0 ? (
          <Button size="sm" onClick={onMapRemaining}>
            {t('mapping.mapRemaining', { count: unmapped })}
          </Button>
        ) : null}
      </div>
    )
  }

  const onKeyDown = (e: KeyboardEvent<HTMLUListElement>) => {
    const cur = pending[Math.min(active, pending.length - 1)]
    if (!cur) return
    switch (e.key.toLowerCase()) {
      case 'j':
        setActive((i) => Math.min(i + 1, pending.length - 1))
        break
      case 'k':
        setActive((i) => Math.max(i - 1, 0))
        break
      case 'a':
        apply((s) => acceptSuggestion(s, cur.id))
        break
      case 'r':
        apply((s) => rejectSuggestion(s, cur.id))
        break
      default:
        return
    }
    e.preventDefault()
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap gap-2 px-4 pt-3">
        <Button size="sm" variant="secondary" onClick={() => bulk(0.75)}>
          {t('mapping.acceptAbove', { pct: 75 })}
        </Button>
        <Button size="sm" variant="secondary" onClick={() => bulk(0)}>
          {t('mapping.acceptAll')}
        </Button>
      </div>
      <ul ref={listRef} tabIndex={0} onKeyDown={onKeyDown} aria-label={t('mapping.suggestions')} className="flex flex-col outline-none focus-visible:ring-2 focus-visible:ring-ring">
        {pending.map((m, i) => (
          <li
            key={m.id}
            data-active={i === active || undefined}
            className="flex flex-col gap-1.5 border-t border-border px-4 py-3 data-[active]:bg-surface-muted"
            onMouseEnter={() => setActive(i)}
          >
            <div className="flex items-center gap-2 text-[13px]">
              <span className="font-mono text-xs">{m.sources[0]?.column}</span>
              <span className="text-subtle">→</span>
              <span className="truncate font-mono text-xs">{paths.get(m.targetFieldId)}</span>
              <Badge tone="warning" className="ml-auto">
                {Math.round((m.confidence ?? 0) * 100)}%
              </Badge>
            </div>
            {explain[m.id] ? <p className="text-xs text-muted">{explain[m.id]!.reason}</p> : null}
            {m.transforms.length ? <p className="font-mono text-[11px] text-subtle">{`ⓕ ${m.transforms.map((s) => s.op).join(' → ')}`}</p> : null}
            <div className="flex gap-2">
              <Button size="sm" onClick={() => apply((s) => acceptSuggestion(s, m.id))} aria-label={t('mapping.acceptFor', { column: m.sources[0]?.column })}>
                <Check aria-hidden />
                {t('mapping.accept')}
              </Button>
              <Button size="sm" variant="ghost" onClick={() => apply((s) => rejectSuggestion(s, m.id))} aria-label={t('mapping.rejectFor', { column: m.sources[0]?.column })}>
                <X aria-hidden />
                {t('mapping.reject')}
              </Button>
            </div>
          </li>
        ))}
      </ul>
      <p className="px-4 pb-3 text-[11px] text-subtle">{t('mapping.keysHint')}</p>
    </div>
  )
}
