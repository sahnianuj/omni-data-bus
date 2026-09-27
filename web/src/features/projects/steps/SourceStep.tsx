import { useOutletContext } from 'react-router'
import { RepositoryStep } from '@/features/connections/RepositoryStep'
import type { Project } from '../api'

export function SourceStep() {
  const project = useOutletContext<Project>()
  return <RepositoryStep key={`${project.id}-source`} role="source" project={project} />
}
