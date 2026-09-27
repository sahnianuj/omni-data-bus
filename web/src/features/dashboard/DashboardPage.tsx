import { Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { AsyncBoundary } from '@/components/AsyncBoundary'
import { EmptyState } from '@/components/EmptyState'
import { PageHeader } from '@/components/PageHeader'
import { Button } from '@/components/ui/button'
import { Can } from '@/features/auth/Can'
import { useDashboard } from '@/features/projects/api'
import { ProjectCard } from '@/features/projects/ProjectCard'
import { humanizeCount } from '@/lib/format'

function Tile({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-1.5 rounded-[var(--radius-card)] border border-border bg-surface p-5">
      <span className="text-xs text-muted">{label}</span>
      <span className="text-2xl font-semibold tabular-nums tracking-tight">{value}</span>
    </div>
  )
}

export function DashboardPage() {
  const { t } = useTranslation()
  const dashboard = useDashboard()
  const newButton = (
    <Can role="editor">
      <Button asChild>
        <Link to="/projects?new=1">
          <Plus aria-hidden />
          {t('projects.new')}
        </Link>
      </Button>
    </Can>
  )
  return (
    <div className="flex flex-col gap-6 px-10 py-8">
      <PageHeader title={t('dashboard.title')} description={t('dashboard.description')} actions={newButton} />
      <AsyncBoundary query={dashboard}>
        {({ stats, recent }) => (
          <>
            <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
              <Tile label={t('dashboard.projects')} value={String(stats.projects)} />
              <Tile label={t('dashboard.runningJobs')} value={String(stats.runningJobs)} />
              <Tile label={t('dashboard.docsMigrated')} value={humanizeCount(stats.docsMigrated30d)} />
              <Tile label={t('dashboard.repositories')} value={`${stats.repositoriesHealthy} / ${stats.repositoriesTotal}`} />
            </div>
            {recent.length === 0 ? (
              <EmptyState title={t('dashboard.emptyTitle')} body={t('dashboard.emptyBody')} action={newButton} />
            ) : (
              <div className="flex flex-col gap-3">
                <div className="flex items-center justify-between">
                  <h2 className="text-sm font-semibold">{t('dashboard.continue')}</h2>
                  <Link to="/projects" className="text-[13px] font-medium text-primary-soft-foreground hover:underline">
                    {t('dashboard.viewAll')}
                  </Link>
                </div>
                <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
                  {recent.map((p) => (
                    <ProjectCard key={p.id} project={p} />
                  ))}
                </div>
              </div>
            )}
          </>
        )}
      </AsyncBoundary>
    </div>
  )
}
