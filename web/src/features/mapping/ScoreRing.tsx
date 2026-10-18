import { useEffect, useState } from 'react'
import { cn } from '@/lib/utils'
import { scoreColor } from './scoreColor'

const colors = { low: 'text-subtle', mid: 'text-primary', high: 'text-success' } as const

/**
 * The score donut (OC-018 AC1); counts up on first render. With `filled`, the done arc splits
 * in two (OC-187): solid for the part filled with data, lighter for the part left empty on
 * purpose; the gap is what's left. The center number stays the whole ratio.
 */
export function ScoreRing({ ratio, filled, size = 56, label }: { ratio: number; filled?: number; size?: number; label: string }) {
  const [shown, setShown] = useState(0)
  useEffect(() => {
    const id = requestAnimationFrame(() => setShown(1))
    return () => cancelAnimationFrame(id)
  }, [ratio, filled])
  const pct = Math.round(ratio * 100)
  const r = size / 2 - 5
  const circ = 2 * Math.PI * r
  const solid = Math.min(filled ?? ratio, ratio)
  const light = ratio - solid
  const transition = { transition: 'stroke-dasharray 700ms cubic-bezier(.2,.8,.2,1), stroke-dashoffset 700ms cubic-bezier(.2,.8,.2,1)' }
  const arc = (from: number, len: number) => ({
    strokeDasharray: `${circ * len * shown} ${circ}`,
    strokeDashoffset: -circ * from * shown,
  })
  return (
    <div className={cn('relative shrink-0', colors[scoreColor(ratio)])} style={{ width: size, height: size }} role="img" aria-label={label}>
      <svg width={size} height={size} className="-rotate-90">
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="currentColor" strokeOpacity={0.15} strokeWidth={5} />
        {light > 0 ? (
          <circle
            data-testid="ring-not-populated"
            data-length={light.toFixed(4)}
            cx={size / 2}
            cy={size / 2}
            r={r}
            fill="none"
            stroke="currentColor"
            strokeOpacity={0.4}
            strokeWidth={5}
            style={{ ...arc(solid, light), ...transition }}
          />
        ) : null}
        {solid > 0 ? (
          <circle
            data-testid="ring-filled"
            data-length={solid.toFixed(4)}
            cx={size / 2}
            cy={size / 2}
            r={r}
            fill="none"
            stroke="currentColor"
            strokeWidth={5}
            strokeLinecap={light > 0 ? 'butt' : 'round'}
            style={{ ...arc(0, solid), ...transition }}
          />
        ) : null}
      </svg>
      <span className="absolute inset-0 flex items-center justify-center text-[13px] font-semibold text-foreground">{pct}%</span>
    </div>
  )
}
