/** "just now", "5 minutes ago", "3 hours ago", "2 days ago", or a date. */
export function relativeTime(iso: string | undefined, now: Date = new Date()): string {
  if (!iso) return ''
  const then = new Date(iso)
  const secs = Math.round((now.getTime() - then.getTime()) / 1000)
  const rtf = new Intl.RelativeTimeFormat('en', { numeric: 'auto' })
  if (secs < 45) return 'just now'
  if (secs < 3600) return rtf.format(-Math.round(secs / 60), 'minute')
  if (secs < 86400) return rtf.format(-Math.round(secs / 3600), 'hour')
  if (secs < 30 * 86400) return rtf.format(-Math.round(secs / 86400), 'day')
  return then.toLocaleDateString('en', { year: 'numeric', month: 'short', day: 'numeric' })
}

/** 999, 1.2k, 1.2M, 3.4B */
export function humanizeCount(n: number): string {
  if (n < 1000) return String(n)
  const units = [
    [1e9, 'B'],
    [1e6, 'M'],
    [1e3, 'k'],
  ] as const
  for (const [v, s] of units) if (n >= v) return `${(n / v).toFixed(n / v >= 100 ? 0 : 1).replace(/\.0$/, '')}${s}`
  return String(n)
}

/** 512 B, 80 KB, 29.5 MB, 1.2 GB (binary units). */
export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB', 'PB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v >= 100 ? 0 : 1).replace(/\.0$/, '')} ${units[i]}`
}

/** Durations like 37s, 4m 05s, 2h 03m (OC-028). */
export function formatDuration(totalSeconds: number): string {
  const s = Math.max(0, Math.round(totalSeconds))
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, '0')}s`
  const h = Math.floor(m / 60)
  return `${h}h ${String(m % 60).padStart(2, '0')}m`
}

/** A rate like 21.4k docs/s → "21.4k". */
export function formatRate(n: number): string {
  if (n < 1000) return n < 10 ? n.toFixed(1) : String(Math.round(n))
  if (n < 1_000_000) return `${(n / 1000).toFixed(1)}k`
  return `${(n / 1_000_000).toFixed(2)}M`
}
