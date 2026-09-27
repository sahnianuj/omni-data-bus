import { Navigate, useOutletContext } from 'react-router'
import type { Project } from '@/features/projects/api'

/** Step 6: the project's latest job, or the Review step when nothing has run yet. */
export function MigrateStep() {
  const project = useOutletContext<Project>()
  const to = project.latestJob ? `/projects/${project.id}/jobs/${project.latestJob.id}` : `/projects/${project.id}/review`
  return <Navigate to={to} replace />
}
