import { History } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { AsyncBoundary } from '@/components/AsyncBoundary'
import { EmptyState } from '@/components/EmptyState'
import { Badge } from '@/components/ui/badge'
import { relativeTime } from '@/lib/format'
import { useExportHistory, type ExportRecord } from './api'

function Changed({ e }: { e: ExportRecord }) {
  const { t } = useTranslation()
  if (e.changed.length === 0) return <Badge tone="success">{t('export.history.current')}</Badge>
  return (
    <Badge tone="warning" title={t('export.history.changedHint', { list: e.changed.join(', '), sha: e.bundleSha256.slice(0, 12) })}>
      {t('export.history.changed')}
    </Badge>
  )
}

/** Who exported what, when and how (OC-055 AC3); never the file or a secret. */
export function ExportHistory({ projectId }: { projectId: string }) {
  const { t } = useTranslation()
  const history = useExportHistory(projectId)
  return (
    <AsyncBoundary query={history} isEmpty={(xs) => xs.length === 0} empty={<EmptyState icon={<History />} title={t('export.history.emptyTitle')} body={t('export.history.emptyBody')} />}>
      {(items) => (
        <div className="overflow-auto rounded-[var(--radius-card)] border border-border">
          <table className="w-full border-collapse text-[13px]">
            <thead className="bg-surface-muted text-left text-xs text-muted">
              <tr>
                {['when', 'who', 'mappings', 'target', 'runtime', 'version', 'sha', 'compare'].map((c) => (
                  <th key={c} scope="col" className="px-3 py-2 font-medium">{t(`export.history.col.${c}`)}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {items.map((e) => (
                <tr key={e.id} className="border-t border-border">
                  <td className="px-3 py-2 text-muted" title={e.createdAt}>{relativeTime(e.createdAt)}</td>
                  <td className="px-3 py-2">{e.createdByEmail}</td>
                  <td className="px-3 py-2 tabular-nums">{e.mappingIds.length}</td>
                  <td className="px-3 py-2">{t(`projects.types.${e.targetType}`)}</td>
                  <td className="px-3 py-2">{e.runtime === 'script' ? 'migrate.sh' : '.odb.json'}</td>
                  <td className="px-3 py-2 font-mono text-xs">{e.odbVersion}</td>
                  <td className="px-3 py-2 font-mono text-xs" title={e.bundleSha256}>{e.bundleSha256.slice(0, 12)}</td>
                  <td className="px-3 py-2"><Changed e={e} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </AsyncBoundary>
  )
}
