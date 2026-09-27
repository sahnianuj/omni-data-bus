import { useEffect, useState } from 'react'
import { cn } from '@/lib/utils'
import { scoreColor } from './scoreColor'

const colors = { low: 'text-subtle', mid: 'text-primary', high: 'text-success' } as const

/** The auto-map score donut (OC-018 AC1); counts up on first render. */
export function ScoreRing({ ratio, size = 56, label }: { ratio: number; size?: number; label: string }) {
  const [shown, setShown] = useState(0)
  useEffect(() => {
    const id = requestAnimationFrame(() => setShown(ratio))
    return () => cancelAnimationFrame(id)
  }, [ratio])
  const pct = Math.round(ratio * 100)
  const r = size / 2 - 5
  const circ = 2 * Math.PI * r
  return (
    <div className={cn('relative shrink-0', colors[scoreColor(ratio)])} style={{ width: size, height: size }} role="img" aria-label={label}>
      <svg width={size} height={size} className="-rotate-90">
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="currentColor" strokeOpacity={0.15} strokeWidth={5} />
        <circle
          cx={size / 2}
          cy={size / 2}
          r={r}
          fill="none"
          stroke="currentColor"
          strokeWidth={5}
          strokeLinecap="round"
          strokeDasharray={circ}
          strokeDashoffset={circ * (1 - shown)}
          style={{ transition: 'stroke-dashoffset 700ms cubic-bezier(.2,.8,.2,1)' }}
        />
      </svg>
      <span className="absolute inset-0 flex items-center justify-center text-[13px] font-semibold text-foreground">{pct}%</span>
    </div>
  )
}
