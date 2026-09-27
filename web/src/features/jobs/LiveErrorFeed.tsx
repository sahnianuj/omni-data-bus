import { useTranslation } from 'react-i18next'
import type { ErrorSample } from './api'
import { describeError } from './errorCatalog'

export const FEED_SIZE = 20

/** Adds new samples, newest first, keeping the latest 20 (OC-028 AC5). */
export function addSamples(feed: ErrorSample[], incoming: ErrorSample[]): ErrorSample[] {
  const sorted = [...incoming].sort((a, b) => b.at.localeCompare(a.at))
  return [...sorted, ...feed].slice(0, FEED_SIZE)
}

const key = (k: unknown) => (Array.isArray(k) ? `[${k.map(String).join(', ')}]` : k == null ? '' : String(k))

export function LiveErrorFeed({ samples }: { samples: ErrorSample[] }) {
  const { t } = useTranslation()
  if (samples.length === 0) return null
  return (
    <section aria-label={t('monitor.liveErrors')} className="flex flex-col gap-1.5">
      <h3 className="text-xs font-medium uppercase tracking-wide text-subtle">{t('monitor.liveErrors')}</h3>
      <ol className="flex flex-col font-mono text-xs">
        {samples.map((s, i) => (
          <li key={`${s.at}-${i}`} className="grid grid-cols-[5.5rem_8rem_12rem_minmax(0,1fr)_auto] gap-2 border-b border-border py-1.5 last:border-b-0">
            <span className="text-subtle tabular-nums">{new Date(s.at).toLocaleTimeString('en', { hour12: false })}</span>
            <span className="truncate">{s.table}</span>
            <span title={s.code} className="truncate text-warning">{describeError(s.code).title}</span>
            <span className="truncate text-muted">{s.fieldPath ? `${s.fieldPath}: ` : ''}{s.message}</span>
            <span className="text-subtle">{key(s.sourceKey)}</span>
          </li>
        ))}
      </ol>
    </section>
  )
}
