import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

export type TileTone = 'neutral' | 'warning' | 'danger'

/** A labelled figure (OC-028): value, a supporting line, and optional extra content. */
export function StatTile({ label, value, sub, tone = 'neutral', children }: { label: string; value: ReactNode; sub?: ReactNode; tone?: TileTone; children?: ReactNode }) {
  return (
    <section
      aria-label={label}
      data-tone={tone}
      className={cn(
        'flex min-w-0 flex-col gap-1 rounded-[var(--radius-card)] border bg-surface p-4',
        tone === 'neutral' && 'border-border',
        tone === 'warning' && 'border-[var(--warning-dot)] bg-warning-soft',
        tone === 'danger' && 'border-danger bg-danger-soft',
      )}
    >
      <h3 className="text-xs font-medium text-muted">{label}</h3>
      <p className="text-2xl font-semibold tabular-nums">{value}</p>
      {sub ? <p className="text-xs text-muted">{sub}</p> : null}
      {children}
    </section>
  )
}

/** The Failed tile turns amber when anything failed and red past half the error limit (AC5). */
export function failedTone(failed: number, maxErrors: number | undefined): TileTone {
  if (failed <= 0) return 'neutral'
  if (maxErrors != null && maxErrors >= 0 && failed > maxErrors / 2) return 'danger'
  return 'warning'
}
