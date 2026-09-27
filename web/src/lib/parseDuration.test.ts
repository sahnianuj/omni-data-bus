import { describe, expect, it } from 'vitest'
import { parseDuration } from './parseDuration'

describe('parseDuration', () => {
  it('AC6: units and invalid inputs', () => {
    expect(parseDuration('30d')).toBe(2592000)
    expect(parseDuration('12h')).toBe(43200)
    expect(parseDuration('90m')).toBe(5400)
    expect(parseDuration('3600')).toBe(3600)
    expect(parseDuration(' 45S ')).toBe(45)
    expect(parseDuration('-1')).toBeNull()
    expect(parseDuration('abc')).toBeNull()
    expect(parseDuration('1.5h')).toBeNull()
    expect(parseDuration('')).toBeNull()
  })
})
