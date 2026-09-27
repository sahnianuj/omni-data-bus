import { ScrollText, ShieldAlert } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { EmptyState } from '@/components/EmptyState'
import { ErrorState } from '@/components/ErrorState'
import { PageHeader } from '@/components/PageHeader'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { hasRole } from '@/features/auth/Can'
import { useSession } from '@/features/auth/api'
import { relativeTime } from '@/lib/format'
import { useAuditLog, type AuditEvent } from './api'

/** Action categories for the filter; each is an action prefix. */
const categories = ['', 'auth.', 'project.', 'connection.', 'keyspace.', 'mapping.', 'job.', 'report.'] as const

const selectClass =
  'h-9 rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 text-sm focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/30'

function details(e: AuditEvent): string {
  const m = (e.metadata ?? {}) as Record<string, unknown>
  return Object.entries(m)
    .map(([k, v]) => `${k}=${String(v)}`)
    .join(' · ')
}

/** Settings → Audit log (OC-033 AC7): who did what, filterable, for workspace owners. */
export function AuditLogPage() {
  const { t } = useTranslation()
  const session = useSession()
  const owner = hasRole(session.data?.role, 'owner')
  const [action, setAction] = useState('')
  const [mine, setMine] = useState(false)
  const me = session.data?.user.id
  const log = useAuditLog({ action, userId: mine ? me : undefined }, owner)
  const events = log.data?.pages.flatMap((p) => p.items) ?? []
  const actor = (id: string) => (id === me ? t('audit.you') : id ? id.slice(0, 8) : t('audit.system'))

  if (session.data && !owner) {
    return (
      <div className="flex flex-col gap-6 px-10 py-8">
        <PageHeader title={t('audit.title')} description={t('audit.description')} />
        <EmptyState icon={<ShieldAlert />} title={t('audit.ownersOnly')} body={t('audit.ownersOnlyBody')} />
      </div>
    )
  }
  return (
    <div className="flex flex-col gap-6 px-10 py-8">
      <PageHeader title={t('audit.title')} description={t('audit.description')} />
      <div className="flex flex-wrap items-center gap-3">
        <label className="flex items-center gap-2 text-[13px]">
          {t('audit.filterAction')}
          <select className={selectClass} value={action} onChange={(e) => setAction(e.target.value)}>
            {categories.map((c) => (
              <option key={c} value={c}>
                {t(`audit.category.${c ? c.slice(0, -1) : 'all'}`)}
              </option>
            ))}
          </select>
        </label>
        <label className="flex items-center gap-2 text-[13px]">
          <input type="checkbox" checked={mine} onChange={(e) => setMine(e.target.checked)} />
          {t('audit.onlyMine')}
        </label>
      </div>
      {log.isPending ? (
        <div className="flex flex-col gap-2">
          <Skeleton className="h-8" />
          <Skeleton className="h-8" />
          <Skeleton className="h-8" />
        </div>
      ) : log.isError ? (
        <ErrorState error={log.error} onRetry={() => void log.refetch()} />
      ) : events.length === 0 ? (
        <EmptyState icon={<ScrollText />} title={t('audit.emptyTitle')} body={t('audit.emptyBody')} />
      ) : (
        <div className="overflow-auto rounded-[var(--radius-card)] border border-border">
          <table className="w-full border-collapse text-[13px]">
            <thead className="bg-surface-muted text-left text-xs text-muted">
              <tr>
                {(['when', 'who', 'action', 'entity', 'details'] as const).map((c) => (
                  <th key={c} scope="col" className="px-3 py-2 font-medium">
                    {t(`audit.col.${c}`)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {events.map((e) => (
                <tr key={e.id} className="border-t border-border">
                  <td className="whitespace-nowrap px-3 py-2 text-muted" title={new Date(e.createdAt).toLocaleString()}>
                    {relativeTime(e.createdAt)}
                  </td>
                  <td className="px-3 py-2">{actor(e.userId)}</td>
                  <td className="px-3 py-2 font-mono text-xs">{e.action}</td>
                  <td className="px-3 py-2 font-mono text-xs text-muted">
                    {e.entityType}
                    {e.entityId ? ` ${e.entityId.slice(0, 12)}` : ''}
                  </td>
                  <td className="px-3 py-2 text-xs text-muted">{details(e)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {log.hasNextPage ? (
        <Button variant="secondary" size="sm" className="self-start" disabled={log.isFetchingNextPage} onClick={() => void log.fetchNextPage()}>
          {t('audit.loadMore')}
        </Button>
      ) : null}
    </div>
  )
}
