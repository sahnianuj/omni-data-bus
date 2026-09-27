import { AlertCircle, CheckCircle2, Circle, Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { cn } from '@/lib/utils'
import type { MappingProgress } from './api'

const fmt = new Intl.NumberFormat('en')

function PhaseIcon({ phase }: { phase: MappingProgress['phase'] }) {
  switch (phase) {
    case 'done':
      return <CheckCircle2 className="size-4 text-success-dot" aria-hidden />
    case 'failed':
      return <AlertCircle className="size-4 text-danger" aria-hidden />
    case 'migrating':
    case 'preparing':
    case 'verifying':
      return <Loader2 className="size-4 animate-spin text-primary" aria-hidden />
    default:
      return <Circle className="size-4 text-subtle" aria-hidden />
  }
}

/** One row per mapping, with its phase and a mini bar (the Tables tab). */
export function MappingProgressList({ projectId, mappings }: { projectId: string; mappings: MappingProgress[] }) {
  const { t } = useTranslation()
  return (
    <ul aria-label={t('monitor.tables')} className="flex flex-col divide-y divide-border rounded-[var(--radius-card)] border border-border bg-surface">
      {mappings.map((m) => (
        <li key={m.mappingId} className="grid grid-cols-[1.25rem_minmax(0,1.3fr)_minmax(0,1fr)_7rem_8rem] items-center gap-3 px-4 py-2.5 text-[13px]">
          <PhaseIcon phase={m.phase} />
          <Link to={`/projects/${projectId}/mappings/${m.mappingId}`} className="min-w-0 truncate hover:underline">
            <span className="font-medium">{m.table}</span>
            <span className="text-subtle"> → {m.target}</span>
          </Link>
          <span className="text-right font-mono text-xs tabular-nums text-muted">
            {m.phase === 'queued' && m.processed === 0 ? '—' : fmt.format(m.processed)} / {fmt.format(m.total)}
          </span>
          <span className="flex items-center gap-2">
            <span className="h-1.5 flex-1 overflow-hidden rounded-full bg-surface-muted">
              <span className={cn('block h-full rounded-full', m.phase === 'done' ? 'bg-[var(--success-dot)]' : m.phase === 'failed' ? 'bg-[var(--danger)]' : 'bg-[var(--primary)]')} style={{ width: `${Math.min(100, m.percent)}%` }} />
            </span>
            <span className="w-9 text-right font-mono text-xs tabular-nums">{Math.round(m.percent)}%</span>
          </span>
          <span className="text-xs text-muted">{t(`monitor.phase.${m.phase}`)}</span>
        </li>
      ))}
    </ul>
  )
}
