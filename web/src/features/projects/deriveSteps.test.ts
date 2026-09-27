import { describe, expect, it } from 'vitest'
import { makeProject, repo } from '@/test/projects'
import { deriveSteps, firstIncompleteStep } from './steps'

const states = (p: Parameters<typeof deriveSteps>[0], cur: Parameters<typeof deriveSteps>[1]) =>
  Object.fromEntries(deriveSteps(p, cur).map((s) => [s.id, s.state]))

describe('deriveSteps', () => {
  it('AC5: new project — source current, later steps locked', () => {
    expect(states(makeProject(), 'source')).toEqual({
      source: 'current', target: 'upcoming', tables: 'locked', mappings: 'locked', review: 'locked', migrate: 'locked',
    })
  })

  it('AC5: both repositories tested OK unlock tables; migrate stays locked', () => {
    const p = makeProject({ source: repo('postgres'), target: repo('couchbase', 'warn') })
    expect(states(p, 'tables')).toEqual({
      source: 'complete', target: 'complete', tables: 'current', mappings: 'upcoming', review: 'upcoming', migrate: 'locked',
    })
  })

  it('AC5: a failed test needs attention and keeps later steps locked', () => {
    const p = makeProject({ source: repo('postgres', 'failed'), target: repo('couchbase') })
    const s = states(p, 'target')
    expect(s.source).toBe('attention')
    expect(s.tables).toBe('locked')
  })

  it('AC5: untested repositories are not complete', () => {
    expect(states(makeProject({ source: repo('postgres', 'untested') }), 'target').source).toBe('upcoming')
  })

  it('AC5: resumes at the first incomplete step', () => {
    expect(firstIncompleteStep(makeProject())).toBe('source')
    expect(firstIncompleteStep(makeProject({ source: repo('postgres') }))).toBe('target')
    expect(firstIncompleteStep(makeProject({ source: repo('postgres'), target: repo('couchbase') }))).toBe('tables')
  })

  it('OC-035: tables completes with mappings; mappings completes when all are ready', () => {
    const base = { source: repo('postgres'), target: repo('couchbase') }
    expect(states(makeProject({ ...base, mappings: 2, readyMappings: 1 }), 'review')).toMatchObject({ tables: 'complete', mappings: 'upcoming' })
    expect(states(makeProject({ ...base, mappings: 2, readyMappings: 2 }), 'review')).toMatchObject({ tables: 'complete', mappings: 'complete' })
    expect(firstIncompleteStep(makeProject({ ...base, mappings: 2, readyMappings: 1 }))).toBe('mappings')
    expect(firstIncompleteStep(makeProject({ ...base, mappings: 2, readyMappings: 2 }))).toBe('review')
  })

  it('OC-023: migrate unlocks once a job exists and reflects its outcome', () => {
    const base = { source: repo('postgres'), target: repo('couchbase'), mappings: 1, readyMappings: 1 }
    expect(states(makeProject(base), 'review').migrate).toBe('locked')
    expect(states(makeProject({ ...base, latestJob: { id: 'j', status: 'running' } }), 'review').migrate).toBe('upcoming')
    expect(states(makeProject({ ...base, latestJob: { id: 'j', status: 'running' } }), 'migrate').migrate).toBe('current')
    expect(states(makeProject({ ...base, latestJob: { id: 'j', status: 'completed_with_errors' } }), 'review').migrate).toBe('complete')
    expect(states(makeProject({ ...base, latestJob: { id: 'j', status: 'failed' } }), 'review').migrate).toBe('attention')
    expect(states(makeProject({ ...base, latestJob: { id: 'j', status: 'running' } }), 'migrate').review).toBe('complete')
  })
})
