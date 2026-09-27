import { describe, expect, it } from 'vitest'
import config from '../eslint.config.js'

type FlatConfig = { files?: string[]; rules?: Record<string, unknown> }
const configs = config as FlatConfig[]

function ruleFor(name: string, file: string): unknown {
  let value: unknown
  for (const c of configs) {
    const ext = file.split('.').pop() ?? ''
    const applies = !c.files || c.files.some((glob) => new RegExp(`[.{,]${ext}([},]|$)`).test(glob))
    if (applies && c.rules && name in c.rules) value = c.rules[name]
  }
  return value
}

describe('eslint config', () => {
  it('AC3: forbids explicit any', () => {
    expect(ruleFor('@typescript-eslint/no-explicit-any', 'x.ts')).toBe('error')
  })

  it('AC8: i18next/no-literal-string enabled for tsx', () => {
    const rule = ruleFor('i18next/no-literal-string', 'x.tsx') as unknown[]
    expect(rule?.[0]).toBe('error')
  })
})
