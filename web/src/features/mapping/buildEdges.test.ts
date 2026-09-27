import { describe, expect, it } from 'vitest'
import { sampleSpec } from '@/test/mapping'
import { buildEdges, parentMap } from './buildEdges'
import { largeSpec } from './buildEdges.fixture'

describe('buildEdges', () => {
  it('AC1: one edge per source ref with state style', () => {
    const spec = sampleSpec()
    spec.fieldMappings.push({ id: 'm_rej', targetFieldId: 'f_last', sources: [{ column: 'last_name' }], transforms: [], origin: 'suggested', state: 'rejected' })
    spec.fieldMappings.push({ id: 'm_multi', targetFieldId: 'f_last', sources: [{ column: 'first_name' }, { column: 'last_name' }], transforms: [{ op: 'concat' }], origin: 'manual', state: 'active' })
    const edges = buildEdges(spec, new Set(), parentMap(spec), [{ severity: 'error', code: 'X', path: 'createdAt', message: '', fieldMappingId: 'm_created' }])
    expect(edges.map((e) => [e.id, e.state])).toEqual([
      ['m_id:customer_id', 'auto'],
      ['m_first:first_name', 'auto'],
      ['m_email:email_address', 'suggested'],
      ['m_created:created_on', 'error'],
      ['m_multi:first_name', 'manual'],
      ['m_multi:last_name', 'manual'],
    ])
    expect(edges[1]).toMatchObject({ transformCount: 1, firstOp: 'trim', bundle: 1, multi: false })
    expect(edges[4]!.multi).toBe(true)
  })

  it('AC1: bundles edges for collapsed parents', () => {
    const spec = sampleSpec()
    spec.fieldMappings.push({ id: 'm_last', targetFieldId: 'f_last', sources: [{ column: 'first_name' }], transforms: [], origin: 'manual', state: 'active' })
    const edges = buildEdges(spec, new Set(['f_name']), parentMap(spec))
    const bundled = edges.filter((e) => e.to === 'f_name')
    expect(bundled).toHaveLength(1)
    expect(bundled[0]).toMatchObject({ from: 'first_name', bundle: 2 })
  })

  it('AC10 budget: 300 edges in under 5 ms (median of 30)', () => {
    const spec = largeSpec()
    const parents = parentMap(spec)
    const collapsed = new Set(['g3', 'g7', 'g20'])
    expect(buildEdges(spec, new Set(), parents)).toHaveLength(375)
    const times: number[] = []
    for (let i = 0; i < 30; i++) {
      const t0 = performance.now()
      buildEdges(spec, collapsed, parents)
      times.push(performance.now() - t0)
    }
    times.sort((a, b) => a - b)
    expect(times[15]).toBeLessThan(5)
  })
})
