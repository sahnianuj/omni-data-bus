import type { Project } from './api'

export type StepId = 'source' | 'target' | 'tables' | 'mappings' | 'review' | 'migrate'
export type StepState = 'complete' | 'current' | 'upcoming' | 'attention' | 'locked'
export type Step = { id: StepId; state: StepState }

export const stepOrder: StepId[] = ['source', 'target', 'tables', 'mappings', 'review', 'migrate']

const repoState = (r: Project['source']) => (!r ? 'missing' : r.lastTestStatus === 'ok' || r.lastTestStatus === 'warn' ? 'ok' : r.lastTestStatus === 'failed' ? 'failed' : 'untested')

/**
 * Derives the wizard's step states (OC-007 AC5): repositories are complete once saved and tested
 * OK; a failed test needs attention; steps 3–6 stay locked until both repositories pass.
 */
type StepInput = Pick<Project, 'source' | 'target' | 'mappings' | 'readyMappings'> & Partial<Pick<Project, 'latestJob'>>

/**
 * Tables is complete once any mapping exists; Mappings once every mapping is ready (OC-035).
 */
export function deriveSteps(project: StepInput, current: StepId): Step[] {
  const src = repoState(project.source)
  const tgt = repoState(project.target)
  const unlocked = src === 'ok' && tgt === 'ok'
  return stepOrder.map((id): Step => {
    if (id === 'source' || id === 'target') {
      const s = id === 'source' ? src : tgt
      if (s === 'failed') return { id, state: 'attention' }
      if (s === 'ok') return { id, state: id === current ? 'current' : 'complete' }
      return { id, state: id === current ? 'current' : 'upcoming' }
    }
    if (!unlocked) return { id, state: 'locked' }
    if (id === 'migrate') {
      // Unlocked once a migration has been started from Review (OC-023).
      if (!project.latestJob) return { id, state: 'locked' }
      if (id === current) return { id, state: 'current' }
      const st = project.latestJob.status
      if (st === 'completed' || st === 'completed_with_errors') return { id, state: 'complete' }
      if (st === 'failed') return { id, state: 'attention' }
      return { id, state: 'upcoming' }
    }
    if (id === current) return { id, state: 'current' }
    if (id === 'review' && project.latestJob) return { id, state: 'complete' }
    if (id === 'tables' && hasMappings(project)) return { id, state: 'complete' }
    if (id === 'mappings' && allReady(project)) return { id, state: 'complete' }
    return { id, state: 'upcoming' }
  })
}

/** The step to open when entering a project: the first one that isn't done. */
export function firstIncompleteStep(project: StepInput): StepId {
  if (repoState(project.source) !== 'ok') return 'source'
  if (repoState(project.target) !== 'ok') return 'target'
  if (!hasMappings(project)) return 'tables'
  if (!allReady(project)) return 'mappings'
  return 'review'
}

const hasMappings = (p: StepInput) => (p.mappings ?? 0) > 0
const allReady = (p: StepInput) => hasMappings(p) && (p.readyMappings ?? 0) >= (p.mappings ?? 0)
