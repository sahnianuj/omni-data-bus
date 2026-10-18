import { Download, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Can } from '@/features/auth/Can'
import { proposeMapping, type Proposal } from '@/features/mapping/api'
import { useWorkbench, useWorkbenchStore } from '@/features/mapping/store'
import { ApiError } from '@/lib/api/client'
import { cn } from '@/lib/utils'
import { counts, coverageCSV, keepHighOnly, merge, type ReviewFilter } from './review'

/** Starts, keeps and ends a review (OC-168): the proposal is one undo step from start to Apply. */
export function useReview() {
  const store = useWorkbenchStore()
  const start = (proposal: Proposal) => {
    const s = store.getState()
    const mark = s.history.past.length
    s.apply(() => proposal.spec)
    s.setReview({ proposal, mark })
  }
  const keep = () => {
    const s = store.getState()
    if (!s.review) return
    s.squash(s.review.mark)
    s.setReview(null)
  }
  const keepHigh = () => {
    const s = store.getState()
    const r = s.review
    if (!r) return
    s.apply((sp) => keepHighOnly(sp, r))
    keep()
  }
  const discard = () => {
    const s = store.getState()
    if (!s.review) return
    s.revert(s.review.mark)
    s.setReview(null)
  }
  /** Propose again: fill what's missing around the user's edits, keeping them (base). */
  const again = async (nodeId?: string) => {
    const s = store.getState()
    const r = s.review
    const next = await proposeMapping(s.view.projectId, s.view.id, { specOverride: s.history.present, base: true, ...(nodeId ? { nodeId } : {}) })
    s.apply(() => next.spec)
    if (r) s.setReview({ proposal: merge(r.proposal, next), mark: r.mark })
    else s.setReview({ proposal: next, mark: s.history.past.length - 1 })
  }
  return { start, keep, keepHigh, discard, again }
}

const filters: ReviewFilter[] = ['all', 'review', 'unresolved', 'notPopulated', 'edited']

/** The review's banner: what was proposed, filters, and Apply / Apply high only / Discard. */
export function ReviewBanner({ filter, onFilter }: { filter: ReviewFilter; onFilter: (f: ReviewFilter) => void }) {
  const { t } = useTranslation()
  const review = useWorkbench((s) => s.review)
  const spec = useWorkbench((s) => s.history.present)
  const name = useWorkbench((s) => s.view.sourceTable)
  const { keep, keepHigh, discard, again } = useReview()
  const [busy, setBusy] = useState(false)
  if (!review) return null
  const c = counts(spec, review)
  const driving = review.proposal.driving[0]
  const download = () => {
    const url = URL.createObjectURL(new Blob([coverageCSV(spec, review)], { type: 'text/csv' }))
    const a = document.createElement('a')
    a.href = url
    a.download = `${name || 'mapping'}-coverage.csv`
    a.click()
    URL.revokeObjectURL(url)
  }
  const recompute = async () => {
    setBusy(true)
    try {
      await again()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <section aria-label={t('propose.title')} className="flex flex-col gap-2 border-b border-border bg-primary-soft px-4 py-2 text-[13px] text-primary-soft-foreground">
      <div className="flex flex-wrap items-center gap-2">
        <strong>{t('propose.title')}</strong>
        <span role="status" aria-live="polite">
          {t('propose.summary', { proposed: c.proposed, fields: c.fields, review: c.review, notPopulated: c.notPopulated, unresolved: c.unresolved })}
        </span>
        {driving ? <span className="text-xs text-muted">{t('propose.driving', { table: `${driving.table.schema}.${driving.table.name}` })}</span> : null}
        <span className="ml-auto" />
        <Button size="sm" variant="ghost" onClick={download}>
          <Download aria-hidden />
          {t('propose.sheet')}
        </Button>
        <Can role="editor">
          <span className="flex flex-wrap items-center gap-2">
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => void recompute()}>
            <RefreshCw aria-hidden />
            {t('propose.again')}
          </Button>
          <Button size="sm" variant="ghost" onClick={discard}>
            {t('propose.discard')}
          </Button>
          <Button size="sm" variant="secondary" onClick={keepHigh}>
            {t('propose.applyHigh')}
          </Button>
          <Button size="sm" onClick={keep}>
            {t('propose.apply')}
          </Button>
          </span>
        </Can>
      </div>
      <div role="radiogroup" aria-label={t('propose.filter')} className="flex flex-wrap gap-1">
        {filters.map((f) => (
          <button
            key={f}
            type="button"
            role="radio"
            aria-checked={filter === f}
            onClick={() => onFilter(f)}
            className={cn('rounded-full border px-2 py-0.5 text-xs', filter === f ? 'border-primary bg-primary text-primary-foreground' : 'border-border bg-surface text-foreground hover:bg-surface-muted')}
          >
            {t(`propose.filters.${f}`)}
            {f === 'review' ? ` (${c.review})` : f === 'unresolved' ? ` (${c.unresolved})` : f === 'edited' ? ` (${c.edited})` : ''}
          </button>
        ))}
        <span className="ml-2 self-center text-xs text-muted">{t('propose.keys')}</span>
      </div>
    </section>
  )
}
