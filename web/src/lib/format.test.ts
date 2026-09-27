import { describe, expect, it } from 'vitest'
import { formatBytes, formatDuration, formatRate, humanizeCount, relativeTime } from './format'

describe('format', () => {
  it('humanizeCount 999, 1.2k, 1.2M, 3.4B', () => {
    expect(humanizeCount(999)).toBe('999')
    expect(humanizeCount(1200)).toBe('1.2k')
    expect(humanizeCount(1_000)).toBe('1k')
    expect(humanizeCount(1_234_567)).toBe('1.2M')
    expect(humanizeCount(3_400_000_000)).toBe('3.4B')
    expect(humanizeCount(120_345)).toBe('120k')
  })

  it('formatBytes', () => {
    expect(formatBytes(512)).toBe('512 B')
    expect(formatBytes(81920)).toBe('80 KB')
    expect(formatBytes(30_932_992)).toBe('29.5 MB')
    expect(formatBytes(1.2 * 1024 ** 3)).toBe('1.2 GB')
    expect(formatBytes(150 * 1024)).toBe('150 KB')
  })

  it('relativeTime', () => {
    const now = new Date('2026-09-25T12:00:00Z')
    expect(relativeTime('2026-09-25T11:59:30Z', now)).toBe('just now')
    expect(relativeTime('2026-09-25T11:55:00Z', now)).toBe('5 minutes ago')
    expect(relativeTime('2026-09-25T09:00:00Z', now)).toBe('3 hours ago')
    expect(relativeTime('2026-09-23T12:00:00Z', now)).toBe('2 days ago')
    expect(relativeTime('2026-01-02T12:00:00Z', now)).toMatch(/2026/)
    expect(relativeTime(undefined, now)).toBe('')
  })
})

describe('formatDuration', () => {
  it('AC1: formatDuration 37s, 4m 05s, 2h 03m', () => {
    expect(formatDuration(37)).toBe('37s')
    expect(formatDuration(245)).toBe('4m 05s')
    expect(formatDuration(2 * 3600 + 3 * 60 + 59)).toBe('2h 03m')
    expect(formatDuration(-3)).toBe('0s')
    expect(formatRate(21_450.5)).toBe('21.5k')
    expect(formatRate(7.25)).toBe('7.3')
    expect(formatRate(1_500_000)).toBe('1.50M')
  })
})
