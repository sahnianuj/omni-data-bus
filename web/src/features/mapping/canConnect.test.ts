import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { canConnect, compatMatrix } from './canConnect'

const fixture = JSON.parse(readFileSync(resolve(process.cwd(), '../backend/internal/mapping/compat.json'), 'utf8')) as {
  types: string[]
  matrix: Record<string, Record<string, { class: string; transform?: unknown }>>
}

describe('canConnect', () => {
  it('AC2: matrix parity with server fixture', () => {
    for (const src of fixture.types) {
      for (const dst of fixture.types) {
        expect(compatMatrix[src]?.[dst], `${src} → ${dst}`).toEqual(fixture.matrix[src]![dst])
      }
    }
  })

  it('AC2: allowed, class and suggested conversion', () => {
    expect(canConnect('string', 'integer')).toEqual({ allowed: true, class: 'lossy', suggestedTransform: { op: 'parseInteger' } })
    expect(canConnect('string', 'object')).toEqual({ allowed: false, class: 'incompatible', suggestedTransform: undefined })
    expect(canConnect('any', 'object').class).toBe('exact')
    expect(canConnect('mystery', 'string').allowed).toBe(false)
  })
})
