import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mockPreview, renderWorkbench } from '@/test/mapping'
import { setupMswServer } from '@/test/msw'
import { mockSession } from '@/test/session'
import { Inspector } from './Inspector'
import type { MappingPreview } from './types'

setupMswServer()
beforeEach(() => mockSession('owner'))

const preview = (after: string[]): MappingPreview => ({
  writeMode: 'upsert',
  fields: [' Ada ', 'Linus', 'Grace '].map((before, i) => ({ before, after: after[i], steps: [] })),
})

describe('Inspector', () => {
  it('AC8: shows sources, explain, preview', async () => {
    const bodies = mockPreview(preview(['ADA', 'LINUS', 'GRACE']))
    const { store } = renderWorkbench(<Inspector projectId="p1" />)
    act(() => store.getState().select({ kind: 'mapping', id: 'm_id' }))

    const inspector = screen.getByRole('complementary', { name: 'Inspector' })
    expect(within(inspector).getByRole('heading', { level: 3 })).toHaveTextContent('customer_id→customerId')
    const why = within(inspector).getByRole('region', { name: 'Why this match' })
    expect(why).toHaveTextContent('Names match ignoring case and separators (97%); types match')
    expect(why).toHaveTextContent('NORMALIZED_EQUAL +0.97')

    const pv = within(inspector).getByRole('region', { name: 'Preview' })
    await waitFor(() => expect(within(pv).getAllByRole('listitem')).toHaveLength(3))
    expect(within(pv).getAllByRole('listitem')[0]).toHaveTextContent('" Ada "→"ADA"')
    expect(bodies).toHaveLength(1)
    expect(bodies[0]).toMatchObject({ fieldMappingId: 'm_id', limit: 3 })

    // A suggested mapping explains itself but has no preview until accepted.
    act(() => store.getState().select({ kind: 'mapping', id: 'm_email' }))
    expect(within(inspector).getByRole('region', { name: 'Why this match' })).toHaveTextContent('SYNONYM +0.88')
    expect(within(inspector).queryByRole('region', { name: 'Preview' })).not.toBeInTheDocument()
  })

  describe('with fake timers', () => {
    beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }))
    afterEach(() => vi.useRealTimers())

    it('AC8: debounced preview refresh on transform change', async () => {
      const bodies = mockPreview(preview(['Ada', 'Linus', 'Grace']))
      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
      const { store } = renderWorkbench(<Inspector projectId="p1" />)
      act(() => store.getState().select({ kind: 'mapping', id: 'm_first' }))
      await waitFor(() => expect(bodies).toHaveLength(1))

      // Two quick edits: nothing is sent until 300 ms after the last one.
      await user.click(screen.getByRole('button', { name: 'Add transform' }))
      await user.click(screen.getByRole('button', { name: 'Uppercase' }))
      await user.click(within(screen.getByRole('listitem', { name: 'Step 2: Uppercase' })).getByRole('button', { name: 'Move up' }))
      act(() => vi.advanceTimersByTime(200))
      expect(bodies).toHaveLength(1)

      act(() => vi.advanceTimersByTime(110))
      await waitFor(() => expect(bodies).toHaveLength(2))
      const override = bodies[1]!.specOverride!.fieldMappings.find((m) => m.id === 'm_first')!
      expect(override.transforms).toEqual([{ op: 'upper' }, { op: 'trim' }])
    })
  })
})
