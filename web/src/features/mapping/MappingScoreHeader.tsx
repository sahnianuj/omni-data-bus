import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { AlertTriangle, ChevronDown, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Button } from '@/components/ui/button'
import { Can } from '@/features/auth/Can'
import { cn } from '@/lib/utils'
import { ScoreRing } from './ScoreRing'
import type { Filter } from './selectVisible'
import type { MappingStats } from './types'

type Segment = { key: Filter | 'ignored'; count: number; className: string }

/** Proportional state breakdown of the source columns; each segment filters the workbench (OC-018 AC2). */
export function SegmentBar({ stats, onFilter }: { stats: MappingStats; onFilter: (f: Filter) => void }) {
  const { t } = useTranslation()
  const segs: Segment[] = [
    { key: 'auto', count: stats.autoMapped, className: 'bg-[var(--map-auto)]' },
    { key: 'suggested', count: stats.suggested, className: 'bg-[var(--map-suggested)]' },
    { key: 'manual', count: stats.manuallyMapped, className: 'bg-[var(--map-manual)]' },
    { key: 'unmapped', count: stats.unmappedSource, className: 'bg-[var(--map-unmapped)]' },
  ]
  const total = Math.max(1, stats.totalSourceColumns)
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex h-2 w-full overflow-hidden rounded-full bg-surface-muted">
        {segs.map((s) =>
          s.count > 0 ? (
            <button
              key={s.key}
              type="button"
              data-testid={`segment-${s.key}`}
              style={{ width: `${(s.count / total) * 100}%` }}
              className={cn('h-full hover:opacity-80', s.className)}
              aria-label={t(`mapping.segment.${s.key}`, { count: s.count })}
              onClick={() => onFilter(s.key as Filter)}
            />
          ) : null,
        )}
      </div>
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted">
        {[...segs, { key: 'ignored' as const, count: stats.ignored, className: 'bg-border-strong' }].map((s) => (
          <button key={s.key} type="button" className="flex items-center gap-1.5 hover:text-foreground" onClick={() => s.key !== 'ignored' && onFilter(s.key as Filter)}>
            <span className={cn('size-2 rounded-full', s.className)} aria-hidden />
            {t(`mapping.segment.${s.key}`, { count: s.count })}
          </button>
        ))}
      </div>
    </div>
  )
}

type Props = {
  automap: MappingStats // the snapshot of the last auto-map run: the headline score
  live: MappingStats // recomputed after every edit
  onFilter: (f: Filter) => void
  onRerun: (mode: 'fill' | 'reset') => void
  rerunning?: boolean
  onRequiredClick?: () => void
}

/** The workbench's score header (OC-018 AC1–AC3, AC7). */
export function MappingScoreHeader({ automap, live, onFilter, onRerun, rerunning, onRequiredClick }: Props) {
  const { t } = useTranslation()
  const [confirmReset, setConfirmReset] = useState(false)
  const covered = live.autoMapped + live.manuallyMapped
  return (
    <section aria-label={t('mapping.score')} className="flex items-start gap-4 border-b border-border px-6 py-4">
      <ScoreRing ratio={automap.autoMapRatio} label={t('mapping.autoPercent', { pct: Math.round(automap.autoMapRatio * 100) })} />
      <div className="flex min-w-0 flex-1 flex-col gap-2">
        <h2 className="text-base font-semibold">
          {automap.totalSourceColumns === 0 ? t('mapping.noColumns') : t('mapping.headline', { count: automap.totalSourceColumns, auto: automap.autoMapped })}
        </h2>
        <SegmentBar stats={live} onFilter={onFilter} />
        <p className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted" aria-live="polite">
          <span>{t('mapping.coverage', { pct: Math.round(live.coverageRatio * 100), covered, total: live.totalSourceColumns })}</span>
          {live.requiredTargetUnmapped > 0 ? (
            <button type="button" onClick={onRequiredClick} className="flex items-center gap-1 rounded-full bg-warning-soft px-2 py-0.5 text-warning">
              <AlertTriangle className="size-3" aria-hidden />
              {t('mapping.requiredUnmapped', { count: live.requiredTargetUnmapped })}
            </button>
          ) : null}
          {live.averageConfidence > 0 ? <span>{t('mapping.avgConfidence', { pct: Math.round(live.averageConfidence * 100) })}</span> : null}
          {live.embedded && live.embedded.totalColumns > 0 ? (
            <span data-testid="score-embedded">
              {t('embed.scoreEmbedded', { mapped: live.embedded.autoMapped + live.embedded.manuallyMapped, total: live.embedded.totalColumns })}
            </span>
          ) : null}
        </p>
      </div>
      {/* Each control is wrapped on its own: Can disables its direct child, and a div can't be disabled. */}
      <div className="flex">
        <Can role="editor">
          <Button variant="secondary" size="sm" className="rounded-r-none" loading={rerunning} onClick={() => onRerun('fill')}>
            <RefreshCw aria-hidden />
            {t('mapping.rerun')}
          </Button>
        </Can>
        <DropdownMenu.Root>
          <Can role="editor">
            <DropdownMenu.Trigger asChild>
              <Button variant="secondary" size="sm" className="rounded-l-none border-l-0 px-2" aria-label={t('mapping.rerunOptions')}>
                <ChevronDown aria-hidden />
              </Button>
            </DropdownMenu.Trigger>
          </Can>
          <DropdownMenu.Portal>
            <DropdownMenu.Content align="end" className="z-50 min-w-48 rounded-md border border-border bg-surface p-1 shadow-lg">
              <DropdownMenu.Item className="cursor-default rounded px-2 py-1.5 text-sm outline-none data-[highlighted]:bg-surface-muted" onSelect={() => onRerun('fill')}>
                {t('mapping.fillGaps')}
              </DropdownMenu.Item>
              <DropdownMenu.Item className="cursor-default rounded px-2 py-1.5 text-sm outline-none data-[highlighted]:bg-surface-muted" onSelect={() => setConfirmReset(true)}>
                {t('mapping.resetAuto')}
              </DropdownMenu.Item>
            </DropdownMenu.Content>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
      </div>
      <ConfirmDialog
        open={confirmReset}
        onOpenChange={setConfirmReset}
        title={t('mapping.resetTitle')}
        description={t('mapping.resetBody')}
        confirmLabel={t('mapping.resetConfirm')}
        onConfirm={() => {
          setConfirmReset(false)
          onRerun('reset')
        }}
      />
    </section>
  )
}
