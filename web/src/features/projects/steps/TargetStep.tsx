import { useOutletContext } from 'react-router'
import { RepositoryStep } from '@/features/connections/RepositoryStep'
import type { Project } from '../api'

export function TargetStep() {
  const project = useOutletContext<Project>()
  return <RepositoryStep key={`${project.id}-target`} role="target" project={project} />
}
