import { Navigate, Outlet, useLocation, useParams } from 'react-router'
import { AsyncBoundary } from '@/components/AsyncBoundary'
import { useMaximized } from '@/lib/maximize'
import { useProject } from './api'
import { ProjectHeader } from './ProjectHeader'
import { StepRail } from './StepRail'
import { firstIncompleteStep, stepOrder, type StepId } from './steps'

/** /projects/:projectId — header + step rail + the current step. */
export function ProjectWizard() {
  const { projectId = '' } = useParams()
  const project = useProject(projectId)
  const location = useLocation()
  const maximized = useMaximized()
  const raw = location.pathname.split('/')[3]
  // A job's monitor lives under the Migrate step; Export (OC-055) under Migrate once a migration
  // ran, else under Review, where it starts.
  const seg = (raw === 'jobs' ? 'migrate' : raw === 'export' ? (project.data?.latestJob ? 'migrate' : 'review') : raw) as StepId | undefined
  return (
    <AsyncBoundary query={project}>
      {(p) => {
        if (!seg || !stepOrder.includes(seg)) return <Navigate to={`/projects/${p.id}/${firstIncompleteStep(p)}`} replace />
        return (
          <div className="flex h-full flex-col">
            {maximized ? null : <ProjectHeader key={p.id} project={p} />}
            <div className="flex min-h-0 flex-1">
              <StepRail project={p} current={seg} />
              <section className="min-w-0 flex-1 overflow-y-auto">
                <Outlet context={p} />
              </section>
            </div>
          </div>
        )
      }}
    </AsyncBoundary>
  )
}
