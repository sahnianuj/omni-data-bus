import { FolderKanban, Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useSearchParams } from 'react-router'
import { AsyncBoundary } from '@/components/AsyncBoundary'
import { EmptyState } from '@/components/EmptyState'
import { PageHeader } from '@/components/PageHeader'
import { Button } from '@/components/ui/button'
import { Can } from '@/features/auth/Can'
import { useProjects } from './api'
import { NewProjectDialog } from './NewProjectDialog'
import { ProjectCard } from './ProjectCard'

export function ProjectsPage() {
  const { t } = useTranslation()
  const projects = useProjects()
  const [params, setParams] = useSearchParams()
  const open = params.get('new') === '1'
  const setOpen = (o: boolean) => setParams(o ? { new: '1' } : {}, { replace: true })

  const newButton = (
    <Can role="editor">
      <Button onClick={() => setOpen(true)}>
        <Plus aria-hidden />
        {t('projects.new')}
      </Button>
    </Can>
  )
  return (
    <div className="flex flex-col gap-6 px-10 py-8">
      <PageHeader title={t('projects.title')} description={t('projects.description')} actions={newButton} />
      <AsyncBoundary
        query={projects}
        isEmpty={(items) => items.length === 0}
        empty={<EmptyState icon={<FolderKanban />} title={t('projects.emptyTitle')} body={t('projects.emptyBody')} action={newButton} />}
      >
        {(items) => (
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
            {items.map((p) => (
              <ProjectCard key={p.id} project={p} />
            ))}
          </div>
        )}
      </AsyncBoundary>
      <NewProjectDialog open={open} onOpenChange={setOpen} />
    </div>
  )
}
