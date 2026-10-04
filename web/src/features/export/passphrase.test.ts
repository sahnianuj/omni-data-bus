import { describe, expect, it } from 'vitest'
import { passphraseProblem } from './passphrase'

describe('passphraseProblem (the server rule, OC-052)', () => {
  it('needs 12 characters and enough strength', () => {
    expect(passphraseProblem('short')).toBe('short')
    expect(passphraseProblem('aaaaaaaaaaaaaaaa')).toBe('weak')
    expect(passphraseProblem('abcdefghijklmnop')).toBe('weak')
    expect(passphraseProblem('correct-horse-battery-staple')).toBeNull()
    expect(passphraseProblem('acid-bloom-cradle-dwell-ember-fjord')).toBeNull()
    expect(passphraseProblem('q7#Lm2!vR9pZ')).toBeNull()
  })
})
