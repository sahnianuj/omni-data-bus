import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import '@/i18n'
import type { ErrorSample } from './api'
import { addSamples, LiveErrorFeed } from './LiveErrorFeed'

const sample = (i: number): ErrorSample => ({
  at: new Date(Date.UTC(2026, 8, 1, 15, 4, i)).toISOString(), mappingId: 'm2', table: 'orders', stage: 'transform', code: 'PARSE_FAILED',
  message: `"12,5" is not a number`, fieldPath: 'total', sourceKey: [918270 + i],
})

describe('LiveErrorFeed', () => {
  it('AC5: keeps latest 20 newest first', () => {
    let feed: ErrorSample[] = []
    for (let i = 0; i < 30; i += 3) feed = addSamples(feed, [sample(i), sample(i + 1), sample(i + 2)])
    expect(feed).toHaveLength(20)
    expect(feed[0]!.sourceKey).toEqual([918299])
    expect(feed[19]!.sourceKey).toEqual([918280])
    render(<LiveErrorFeed samples={feed} />)
    const items = within(screen.getByRole('region', { name: 'Live errors (latest)' })).getAllByRole('listitem')
    expect(items).toHaveLength(20)
    expect(items[0]).toHaveTextContent("Value couldn't be converted")
    expect(items[0]).toHaveTextContent('total: "12,5" is not a number')
    expect(items[0]).toHaveTextContent('[918299]')
  })
})
