import { describe, expect, it } from 'vitest'
import { scoreColor } from './scoreColor'

describe('scoreColor', () => {
  it('AC1: thresholds 40/80', () => {
    expect(scoreColor(0.39)).toBe('low')
    expect(scoreColor(0.4)).toBe('mid')
    expect(scoreColor(0.8)).toBe('mid')
    expect(scoreColor(0.81)).toBe('high')
  })
})
