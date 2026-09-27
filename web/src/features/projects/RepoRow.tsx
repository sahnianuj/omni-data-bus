import { useTranslation } from 'react-i18next'
import { StatusDot, type Status } from '@/components/StatusDot'
import type { RepoSummary } from './api'

const dot: Record<RepoSummary['lastTestStatus'], Status> = { ok: 'ok', warn: 'warn', failed: 'failed', untested: 'untested' }
const text: Record<RepoSummary['lastTestStatus'], string> = { ok: 'text-success', warn: 'text-warning', failed: 'text-danger', untested: 'text-subtle' }

export function TypeTile({ type }: { type: string }) {
  return (
    <span className="flex size-[26px] shrink-0 items-center justify-center rounded-md border border-border bg-surface font-mono text-[10px] font-medium text-muted">
      {type === 'postgres' ? 'PG' : 'CB'}
    </span>
  )
}

/** One repository line on a project card: type tile, role, name and test status. */
export function RepoRow({ role, repo }: { role: 'source' | 'target'; repo: RepoSummary }) {
  const { t } = useTranslation()
  const label = t(role === 'source' ? 'projects.sourceLabel' : 'projects.targetLabel', { type: t(`projects.types.${repo.type}`) })
  return (
    <div className="flex items-center gap-2.5">
      <TypeTile type={repo.type} />
      <div className="flex min-w-0 flex-1 flex-col">
        <span className="text-xs text-subtle">{label}</span>
        <span className="truncate font-mono text-xs">{repo.name}</span>
      </div>
      <span className={`flex items-center gap-1.5 text-xs ${text[repo.lastTestStatus]}`}>
        <StatusDot status={dot[repo.lastTestStatus]} />
        {t(`projects.repoStatus.${repo.lastTestStatus}`)}
      </span>
    </div>
  )
}
