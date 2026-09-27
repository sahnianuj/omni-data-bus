import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { opCategories, opSchemas, validateArgs, validPattern } from './opSchemas'

const fixture = JSON.parse(readFileSync(resolve(process.cwd(), '../backend/internal/transform/testdata/op-args.json'), 'utf8')) as {
  cases: { op: string; args: Record<string, unknown>; valid: boolean }[]
}

describe('opSchemas', () => {
  it('AC8: zod schema per op validates args (parity with the Go compilers)', () => {
    for (const c of fixture.cases) {
      expect(Object.keys(validateArgs(c.op, c.args)).length === 0, `${c.op} ${JSON.stringify(c.args)}`).toBe(c.valid)
    }
    expect(Object.keys(opSchemas).sort()).toEqual([...new Set(fixture.cases.map((c) => c.op))].sort())
    expect(opCategories.flatMap((c) => c.ops).sort()).toEqual(Object.keys(opSchemas).sort())
  })

  it('patterns and errors', () => {
    expect(validPattern("yyyy-MM-dd'T'HH:mm")).toBe(true)
    expect(validPattern("'unclosed")).toBe(false)
    expect(validateArgs('nope', {})).toEqual({ op: "Unknown transform 'nope'" })
    expect(validateArgs('split', {})).toEqual({ separator: expect.any(String) })
  })
})
