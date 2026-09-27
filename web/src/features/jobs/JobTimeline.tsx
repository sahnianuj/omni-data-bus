import { AlertTriangle, Camera, CheckCircle2, Gauge, History, ListChecks, Pause, Play, PlayCircle, Rows3, Split, XCircle } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import type { JobEvent } from './api'

const icons: Record<string, ReactNode> = {
  created: <ListChecks aria-hidden />, claimed: <PlayCircle aria-hidden />, snapshot: <Camera aria-hidden />, partitions: <Split aria-hidden />,
  count: <Rows3 aria-hidden />, concurrency: <Gauge aria-hidden />, resumed: <History aria-hidden />, resume: <Play aria-hidden />,
  pause: <Pause aria-hidden />, drained: <Pause aria-hidden />, warning: <AlertTriangle aria-hidden />, 'mapping-done': <CheckCircle2 aria-hidden />,
  complete: <CheckCircle2 aria-hidden />, complete_with_errors: <AlertTriangle aria-hidden />, fail: <XCircle aria-hidden />,
  cancel: <XCircle aria-hidden />, stopped: <XCircle aria-hidden />,
}

/** The Log tab: status changes and notable events, oldest first (OC-028 AC7). */
export function JobTimeline({ events }: { events: JobEvent[] }) {
  const { t } = useTranslation()
  const sorted = [...events].sort((a, b) => a.at.localeCompare(b.at))
  return (
    <ol aria-label={t('monitor.log')} className="relative flex flex-col gap-3 border-l border-border pl-5">
      {sorted.map((e, i) => (
        <li key={`${e.at}-${i}`} data-kind={e.kind} className="relative flex items-start gap-3 text-[13px]">
          <span className="absolute -left-[1.85rem] flex size-5 items-center justify-center rounded-full border border-border bg-surface text-muted [&_svg]:size-3">
            {icons[e.kind] ?? <History aria-hidden />}
          </span>
          <time dateTime={e.at} className="w-20 shrink-0 font-mono text-xs text-subtle tabular-nums">
            {new Date(e.at).toLocaleTimeString('en', { hour12: false })}
          </time>
          <span>{e.message}</span>
        </li>
      ))}
    </ol>
  )
}
