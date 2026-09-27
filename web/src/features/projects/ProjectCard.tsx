import { AlertTriangle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { relativeTime } from '@/lib/format'
import type { Project } from './api'
import { ProjectStatusBadge } from './ProjectStatusBadge'
import { RepoRow } from './RepoRow'

export function ProjectCard({ project }: { project: Project }) {
  const { t } = useTranslation()
  const hasRepos = project.source || project.target
  return (
    <Link
      to={`/projects/${project.id}`}
      className="flex flex-col gap-4 rounded-[var(--radius-card)] border border-border bg-surface p-5 shadow-[0_1px_2px_rgba(0,0,0,0.04)] transition-colors hover:border-border-strong"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 flex-col gap-1">
          <span className="truncate text-[15px] font-semibold">{project.name}</span>
          {project.description ? <span className="line-clamp-2 text-[13px] text-muted">{project.description}</span> : null}
        </div>
        <ProjectStatusBadge status={project.status} />
      </div>
      {hasRepos ? (
        <div className="flex flex-col gap-2 rounded-lg border border-border bg-background p-3">
          {project.source ? <RepoRow role="source" repo={project.source} /> : null}
          {project.target ? <RepoRow role="target" repo={project.target} /> : null}
        </div>
      ) : (
        <div className="flex flex-col gap-2 rounded-lg border border-warning-dot/30 bg-warning-soft p-3">
          <span className="flex items-center gap-2 text-[13px] text-warning">
            <AlertTriangle className="size-4" aria-hidden />
            {t('projects.notSetUp')}
          </span>
          <span className="text-[13px] font-medium text-primary-soft-foreground">{t('projects.setUpSource')}</span>
        </div>
      )}
      <span className="text-[13px] text-subtle">{t('projects.updated', { when: relativeTime(project.updatedAt) })}</span>
    </Link>
  )
}
