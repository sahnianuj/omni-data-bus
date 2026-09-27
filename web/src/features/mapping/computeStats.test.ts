import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { computeStats } from './computeStats'
import type { MappingSpec, MappingStats } from './types'

const fixture = JSON.parse(readFileSync(resolve(process.cwd(), '../backend/internal/mapping/testdata/stats-cases.json'), 'utf8')) as {
  cases: { name: string; columns: string[]; embedded?: Record<string, string[]>; spec: MappingSpec; expected: MappingStats }[]
}

describe('computeStats', () => {
  it('AC3: parity with server fixtures', () => {
    for (const c of fixture.cases) {
      const got = computeStats(c.spec, c.columns, c.embedded ?? undefined)
      for (const k of ['autoMapRatio', 'coverageRatio', 'averageConfidence'] as const) expect(got[k], `${c.name} ${k}`).toBeCloseTo(c.expected[k], 9)
      expect({ ...got, autoMapRatio: 0, coverageRatio: 0, averageConfidence: 0 }, c.name).toEqual({ ...c.expected, autoMapRatio: 0, coverageRatio: 0, averageConfidence: 0 })
    }
  })
})
