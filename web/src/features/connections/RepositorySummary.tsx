import { Lock, Pencil, Zap } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { StatusDot, type Status } from '@/components/StatusDot'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Can } from '@/features/auth/Can'
import { TypeTile } from '@/features/projects/RepoRow'
import { relativeTime } from '@/lib/format'
import { cn } from '@/lib/utils'
import type { ConnectionUI } from './registry'
import type { Connection } from './types'

const statusDot: Record<Connection['lastTestStatus'], Status> = { ok: 'ok', warn: 'warn', failed: 'failed', untested: 'untested' }
const tone = { ok: 'success', warn: 'warning', failed: 'danger', untested: 'neutral' } as const

/** The saved repository: shown after save, re-testable with the stored credentials. */
export function RepositorySummary({
  conn,
  ui,
  testing,
  onTest,
  onEdit,
  actions,
}: {
  conn: Connection
  ui: ConnectionUI
  testing: boolean
  onTest: () => void
  onEdit: () => void
  /** Extra actions on the right of the footer, e.g. "Explore schema →" for a source. */
  actions?: ReactNode
}) {
  const { t } = useTranslation()
  return (
    <section aria-label={conn.name} className="flex flex-col rounded-[var(--radius-card)] border border-border bg-surface">
      <div className="flex items-start gap-3.5 p-5">
        <TypeTile type={conn.type} />
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <div className="flex flex-wrap items-center gap-2.5">
            <span className="text-base font-semibold">{conn.name}</span>
            <Badge tone={tone[conn.lastTestStatus]}>
              <StatusDot status={statusDot[conn.lastTestStatus]} />
              {t(`repo.status.${conn.lastTestStatus}`)}
              {conn.lastTestedAt ? ` · ${t('repo.tested', { when: relativeTime(conn.lastTestedAt) })}` : ''}
            </Badge>
          </div>
          <span className="truncate font-mono text-[13px] text-muted">{ui.endpoint(conn)}</span>
        </div>
      </div>
      <dl className="grid grid-cols-2 gap-4 px-5 pb-5 sm:grid-cols-4">
        {ui.fields(conn).map((f) => (
          // min-w-0 + break-all: a long value (a generated username, a host) wraps inside its column
          // instead of running into the next one.
          <div key={f.labelKey} className="flex min-w-0 flex-col gap-1">
            <dt className="text-xs text-subtle">{t(f.labelKey)}</dt>
            <dd className={cn('break-all text-[13px]', f.mono && 'font-mono')}>{f.valueKey ? t(f.valueKey) : f.value}</dd>
          </div>
        ))}
        <div className="flex flex-col gap-1">
          <dt className="text-xs text-subtle">{t('repo.storedPassword')}</dt>
          <dd className="flex items-center gap-1.5 text-[13px]">
            <Lock className="size-3.5 text-success" aria-hidden />
            {t('repo.storedEncrypted')}
          </dd>
        </div>
      </dl>
      <div className="flex items-center gap-2 rounded-b-[var(--radius-card)] border-t border-border bg-background px-5 py-3.5">
        <Can role="editor">
          <Button variant="secondary" size="sm" loading={testing} onClick={onTest}>
            <Zap aria-hidden />
            {t('repo.test')}
          </Button>
        </Can>
        <Can role="editor">
          <Button variant="ghost" size="sm" onClick={onEdit}>
            <Pencil aria-hidden />
            {t('repo.edit')}
          </Button>
        </Can>
        {actions ? <div className="ml-auto">{actions}</div> : null}
      </div>
    </section>
  )
}
