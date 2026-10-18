import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { computeStats } from './computeStats'
import type { MappingSpec, MappingStats } from './types'
import { upgradeSpec } from './upgradeSpec'

const fixture = JSON.parse(readFileSync(resolve(process.cwd(), '../backend/internal/mapping/testdata/stats-cases.json'), 'utf8')) as {
  cases: { name: string; columns: string[]; embedded?: Record<string, string[]>; spec: MappingSpec; expected: MappingStats }[]
}

describe('computeStats', () => {
  it('AC3: parity with server fixtures', () => {
    for (const c of fixture.cases) {
      // The server's fixtures are 1.0 specs: the client reads them as v2 (OC-037).
      const got = computeStats(upgradeSpec(c.spec), c.columns, c.embedded ?? undefined)
      for (const k of ['autoMapRatio', 'coverageRatio', 'averageConfidence', 'doneRatio'] as const) expect(got[k], `${c.name} ${k}`).toBeCloseTo(c.expected[k], 9)
      expect({ ...got, autoMapRatio: 0, coverageRatio: 0, averageConfidence: 0, doneRatio: 0 }, c.name).toEqual({ ...c.expected, autoMapRatio: 0, coverageRatio: 0, averageConfidence: 0, doneRatio: 0 })
    }
  })

  it('OC-187 AC1: done splits into filled with data and not populated, as the server counts them', () => {
    const c = fixture.cases[fixture.cases.length - 1]!
    const got = computeStats(upgradeSpec(c.spec), c.columns, c.embedded ?? undefined)
    expect([got.targetFieldsFilled, got.targetFieldsNotPopulated, got.targetFieldsDone]).toEqual([5, 2, 7])
    for (const k of fixture.cases) {
      const st = computeStats(upgradeSpec(k.spec), k.columns, k.embedded ?? undefined)
      expect(st.targetFieldsFilled + st.targetFieldsNotPopulated, k.name).toBe(st.targetFieldsDone)
    }
  })
})
