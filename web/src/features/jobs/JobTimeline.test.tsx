import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import '@/i18n'
import { JobTimeline } from './JobTimeline'

describe('JobTimeline', () => {
  it('AC7: renders events in order', () => {
    const at = (s: number) => new Date(Date.UTC(2026, 8, 1, 15, 0, s)).toISOString()
    render(
      <JobTimeline
        events={[
          { at: at(9), kind: 'resumed', message: 'Resumed from checkpoint' },
          { at: at(1), kind: 'created', message: 'Queued 2 tables for migration' },
          { at: at(5), kind: 'concurrency', message: 'Concurrency reduced to 32 (cluster busy)' },
        ]}
      />,
    )
    const items = within(screen.getByRole('list', { name: 'Job log' })).getAllByRole('listitem')
    expect(items.map((i) => i.textContent?.replace(/^\d\d:\d\d:\d\d/, ''))).toEqual([
      'Queued 2 tables for migration', 'Concurrency reduced to 32 (cluster busy)', 'Resumed from checkpoint',
    ])
    expect(items[1]).toHaveAttribute('data-kind', 'concurrency')
    expect(items[1]!.querySelector('svg')).not.toBeNull()
  })
})
