import { readFileSync } from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import { contrast, deltaE } from './contrast'

const css = readFileSync(path.resolve(__dirname, 'tokens.css'), 'utf8')

function tokens(selector: ':root' | '.dark'): Record<string, string> {
  const start = css.indexOf(`${selector} {`)
  const block = css.slice(start, css.indexOf('}', start))
  const out: Record<string, string> = {}
  for (const m of block.matchAll(/--([a-z-]+):\s*(#[0-9a-f]{3,6})/gi)) out[m[1]!] = m[2]!
  return out
}

// [foreground, background] pairs that carry text.
const textPairs: [string, string][] = [
  ['foreground', 'background'],
  ['foreground', 'surface'],
  ['foreground', 'surface-muted'],
  ['muted-foreground', 'surface'],
  ['muted-foreground', 'background'],
  ['subtle-foreground', 'surface'],
  ['primary-foreground', 'primary'],
  ['primary-soft-foreground', 'primary-soft'],
  ['success', 'success-soft'],
  ['warning', 'warning-soft'],
  ['danger', 'danger-soft'],
  ['danger-solid-foreground', 'danger-solid'],
]
// Non-text UI that must be perceivable (focus ring, primary controls).
const uiPairs: [string, string][] = [
  ['ring', 'surface'],
  ['primary', 'surface'],
  ['success-dot', 'surface'],
]

describe.each([':root', '.dark'] as const)('design tokens %s', (theme) => {
  const t = tokens(theme)

  it('AC2: text pairs ≥ 4.5:1', () => {
    for (const [fg, bg] of textPairs) {
      expect(t[fg], `${fg} missing`).toBeDefined()
      expect(contrast(t[fg]!, t[bg]!), `${theme} ${fg} on ${bg}`).toBeGreaterThanOrEqual(4.5)
    }
  })

  it('AC2: UI boundary pairs ≥ 3:1', () => {
    for (const [fg, bg] of uiPairs) {
      expect(contrast(t[fg]!, t[bg]!), `${theme} ${fg} on ${bg}`).toBeGreaterThanOrEqual(3)
    }
  })

  it('AC2: mapping state colors distinguishable', () => {
    const states = ['map-auto', 'map-suggested', 'map-manual', 'map-unmapped', 'map-error']
    for (let i = 0; i < states.length; i++) {
      for (let j = i + 1; j < states.length; j++) {
        expect(deltaE(t[states[i]!]!, t[states[j]!]!), `${states[i]} vs ${states[j]}`).toBeGreaterThanOrEqual(20)
      }
    }
  })
})

describe('contrast helper', () => {
  it('matches known WCAG values', () => {
    expect(contrast('#000000', '#ffffff')).toBeCloseTo(21, 0)
    expect(contrast('#777777', '#ffffff')).toBeCloseTo(4.48, 1)
  })
})
