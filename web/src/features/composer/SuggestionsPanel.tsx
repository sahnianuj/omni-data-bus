import { Link2, Sparkles } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ErrorState } from '@/components/ErrorState'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { suggestBindings, type BindingCandidate, type BindingSuggestion, type BindingSuggestions } from '@/features/mapping/api'
import { useWorkbench } from '@/features/mapping/store'
import { acceptSuggestion, setDrivingTable, type TableRef } from './composerOps'

const key = (t: TableRef) => `${t.schema}.${t.name}`
const tone = { high: 'success', medium: 'warning', unresolved: 'neutral' } as const

/** Accepts every high-confidence suggestion, parents first (the order the server lists them). */
export function acceptAllHigh(spec: Parameters<typeof acceptSuggestion>[0], list: BindingSuggestion[]) {
  return list.filter((s) => s.confidence === 'high').reduce((s, x) => acceptSuggestion(s, x.nodeId, x.candidate), spec)
}

/**
 * Binding suggestions (OC-060): the server proposes the driving table and, for each unbound
 * object and array, a relation (or "map from parent"), with a confidence and reasons. Accepting
 * uses the Bind dialog's operation (one undo step each); the list is recomputed after every
 * change, since a new binding is the scope its children are scored in.
 */
export function SuggestionsPanel({ onClose, onAutoMapAll, onOther }: { onClose: () => void; onAutoMapAll: () => void; onOther: (nodeId: string, table?: TableRef) => void }) {
  const { t } = useTranslation()
  const view = useWorkbench((s) => s.view)
  const spec = useWorkbench((s) => s.history.present)
  const apply = useWorkbench((s) => s.apply)
  const [res, setRes] = useState<BindingSuggestions | null>(null)
  const [error, setError] = useState<unknown>(null)
  const [hidden, setHidden] = useState<Set<string>>(() => new Set())
  const seq = useRef(0)

  useEffect(() => {
    const n = ++seq.current
    suggestBindings(view.projectId, view.id, { root: true, specOverride: spec })
      .then((r) => n === seq.current && (setRes(r), setError(null)))
      .catch((e: unknown) => n === seq.current && setError(e))
  }, [spec, view.projectId, view.id])

  const list = useMemo(() => (res?.suggestions ?? []).filter((s) => !hidden.has(s.nodeId)), [res, hidden])
  const highs = list.filter((s) => s.confidence === 'high')
  const driving = spec.root?.table ?? { schema: view.sourceSchema, name: view.sourceTable }
  const betterRoot = res?.root[0] && key(res.root[0].table) !== key(driving) && res.root[0].score > (res.root.find((r) => key(r.table) === key(driving))?.score ?? 0) ? res.root[0] : undefined
  const hide = (id: string) => setHidden((h) => new Set(h).add(id))

  const accept = (s: BindingSuggestion, c: BindingCandidate) => {
    if (c.fromParent) hide(s.nodeId)
    else apply((sp) => acceptSuggestion(sp, s.nodeId, c))
  }
  const acceptHigh = (thenAutoMap: boolean) => {
    apply((sp) => acceptAllHigh(sp, list))
    highs.filter((s) => s.candidate.fromParent).forEach((s) => hide(s.nodeId))
    if (thenAutoMap) {
      onClose()
      onAutoMapAll()
    }
  }

  const describe = (c: BindingCandidate) =>
    c.fromParent
      ? t('composer.suggest.fromParent')
      : [c.table ? key(c.table) : '', c.kind, c.adapter ? t(`composer.adapter.${c.adapter}`) : '', c.valueColumn ? t('composer.suggest.values', { column: c.valueColumn }) : '', c.via ? t('composer.viaJoin', { via: c.via.table.name }) : (c.join ?? []).map((j) => `${j.parent} = ${j.child}`).join(', ')]
          .filter(Boolean)
          .join(' · ')

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>{t('composer.suggest.title')}</DialogTitle>
          <DialogDescription>{t('composer.suggest.body')}</DialogDescription>
        </DialogHeader>
        {error ? <ErrorState error={error} /> : null}
        {res ? (
          <div className="flex max-h-[60vh] flex-col gap-3 overflow-y-auto text-[13px]">
            {betterRoot ? (
              <div role="status" className="flex flex-wrap items-center gap-2 rounded-md border border-border p-2">
                <span>{t('composer.suggest.root', { table: key(betterRoot.table), reasons: betterRoot.reasons.join(', '), current: key(driving) })}</span>
                <Button size="sm" variant="secondary" className="ml-auto" onClick={() => apply((sp) => setDrivingTable(sp, betterRoot.table))}>
                  {t('composer.suggest.useRoot')}
                </Button>
              </div>
            ) : null}
            <div className="flex flex-wrap gap-2">
              <Button size="sm" variant="secondary" disabled={!highs.length} onClick={() => acceptHigh(false)}>
                {t('composer.suggest.acceptHigh', { count: highs.length })}
              </Button>
              <Button size="sm" disabled={!highs.length} onClick={() => acceptHigh(true)}>
                <Sparkles aria-hidden />
                {t('composer.suggest.acceptHighAutoMap')}
              </Button>
            </div>
            {list.length === 0 ? (
              <p role="status" className="text-muted">
                {t('composer.suggest.none')}
              </p>
            ) : (
              <ul aria-label={t('composer.suggest.list')} className="flex flex-col divide-y divide-border rounded-md border border-border">
                {list.map((s) => (
                  <li key={s.nodeId} aria-label={s.path} className="flex flex-col gap-1.5 p-2">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-mono font-medium">{s.path}</span>
                      <Badge tone={tone[s.confidence]}>{t(`composer.suggest.confidence.${s.confidence}`)}</Badge>
                    </div>
                    {s.confidence === 'unresolved' ? (
                      <p className="text-xs text-muted">{t('composer.suggest.unresolved', { best: s.candidate.table ? describe(s.candidate) : '—' })}</p>
                    ) : (
                      <p className="flex items-center gap-1">
                        <Link2 className="size-3.5 shrink-0 text-primary" aria-hidden />
                        {describe(s.candidate)}
                      </p>
                    )}
                    <p className="text-xs text-muted">{s.candidate.reasons.join(' · ')}</p>
                    <div className="flex flex-wrap gap-2">
                      {s.confidence !== 'unresolved' ? (
                        <Button size="sm" variant="secondary" aria-label={t('composer.suggest.acceptFor', { path: s.path })} onClick={() => accept(s, s.candidate)}>
                          {t('composer.suggest.accept')}
                        </Button>
                      ) : null}
                      {s.alternatives.map((a, i) => (
                        <Button key={i} size="sm" variant="ghost" aria-label={t('composer.suggest.useFor', { path: s.path, what: describe(a) })} onClick={() => accept(s, a)}>
                          {t('composer.suggest.use', { what: a.fromParent ? t('composer.suggest.fromParent') : (a.table?.name ?? '') })}
                        </Button>
                      ))}
                      <Button size="sm" variant="ghost" aria-label={t('composer.suggest.otherFor', { path: s.path })} onClick={() => onOther(s.nodeId, s.candidate.fromParent ? undefined : s.candidate.table)}>
                        {t('composer.suggest.other')}
                      </Button>
                      <Button size="sm" variant="ghost" aria-label={t('composer.suggest.rejectFor', { path: s.path })} onClick={() => hide(s.nodeId)}>
                        {t('composer.suggest.reject')}
                      </Button>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </div>
        ) : !error ? (
          <p role="status" className="text-[13px] text-muted">
            {t('composer.suggest.loading')}
          </p>
        ) : null}
      </DialogContent>
    </Dialog>
  )
}
