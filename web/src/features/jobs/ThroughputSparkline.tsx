import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { formatRate } from '@/lib/format'

export const SPARK_POINTS = 60

/** Appends v, keeping the newest `cap` points (the client-side ring buffer, OC-028 AC3). */
export function pushPoint(points: number[], v: number, cap = SPARK_POINTS): number[] {
  const next = [...points, v]
  return next.length > cap ? next.slice(next.length - cap) : next
}

const W = 160
const H = 36

/** Throughput over the last 60 ticks: a 2px line with a soft fill and a hover readout. */
export function ThroughputSparkline({ points }: { points: number[] }) {
  const { t } = useTranslation()
  const [hover, setHover] = useState<number | null>(null)
  if (points.length < 2) return <div className="h-9" aria-hidden />
  const max = Math.max(...points, 1)
  const x = (i: number) => (i / (SPARK_POINTS - 1)) * W
  const offset = SPARK_POINTS - points.length
  const y = (v: number) => H - 2 - (v / max) * (H - 4)
  const line = points.map((v, i) => `${i ? 'L' : 'M'}${x(i + offset).toFixed(1)},${y(v).toFixed(1)}`).join(' ')
  const area = `${line} L${x(SPARK_POINTS - 1)},${H} L${x(offset)},${H} Z`
  const at = hover ?? points.length - 1
  return (
    <div className="relative">
      <svg
        viewBox={`0 0 ${W} ${H}`}
        className="h-9 w-full"
        role="img"
        aria-label={t('monitor.sparkline', { min: formatRate(Math.min(...points)), max: formatRate(max), n: points.length })}
        onPointerMove={(e) => {
          const r = e.currentTarget.getBoundingClientRect()
          const i = Math.round(((e.clientX - r.left) / r.width) * (SPARK_POINTS - 1)) - offset
          setHover(Math.min(points.length - 1, Math.max(0, i)))
        }}
        onPointerLeave={() => setHover(null)}
      >
        <path d={area} fill="var(--primary)" opacity={0.12} />
        <path d={line} fill="none" stroke="var(--primary)" strokeWidth={2} strokeLinejoin="round" vectorEffect="non-scaling-stroke" />
        <circle cx={x(at + offset)} cy={y(points[at]!)} r={3} fill="var(--primary)" stroke="var(--surface)" strokeWidth={2} />
      </svg>
      {hover != null ? (
        <span className="pointer-events-none absolute -top-6 right-0 rounded bg-foreground px-1.5 py-0.5 text-[11px] text-background tabular-nums">
          {t('jobs.rate', { rate: formatRate(points[hover]!) })}
        </span>
      ) : null}
    </div>
  )
}
