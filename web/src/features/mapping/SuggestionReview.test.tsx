import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { mappingView, renderWorkbench, sampleSpec } from '@/test/mapping'
import { SuggestionReview } from './SuggestionReview'
import type { FieldMapping } from './types'

/** The sample spec plus a second, weaker suggestion (last_name → name.last at 60%). */
function twoSuggestions() {
  const spec = sampleSpec()
  const weak: FieldMapping = { id: 'm_last', targetFieldId: 'f_last', sources: [{ column: 'last_name' }], transforms: [{ op: 'trim' }], origin: 'suggested', state: 'pending', confidence: 0.6 }
  return mappingView({ spec: { ...spec, fieldMappings: [...spec.fieldMappings, weak] } })
}

const state = (store: ReturnType<typeof renderWorkbench>['store'], id: string) => store.getState().history.present.fieldMappings.find((m) => m.id === id)?.state

describe('SuggestionReview', () => {
  it('AC4: sorted with reasons', () => {
    renderWorkbench(<SuggestionReview onMapRemaining={() => {}} />, twoSuggestions())
    const items = within(screen.getByRole('list', { name: 'Suggestions' })).getAllByRole('listitem')
    expect(items[0]).toHaveTextContent('email_address→email78%')
    expect(items[0]).toHaveTextContent("Names are synonyms ('email address' ~ 'email')")
    expect(items[1]).toHaveTextContent('last_name→name.last60%')
    expect(items[1]).toHaveTextContent('ⓕ trim')
  })

  it('AC4: accept/reject update store and stats', async () => {
    const { store } = renderWorkbench(<SuggestionReview onMapRemaining={() => {}} />, twoSuggestions())
    const before = store.getState().stats().suggested
    await userEvent.click(screen.getByRole('button', { name: 'Accept suggestion for email_address' }))
    expect(state(store, 'm_email')).toBe('active')
    await userEvent.click(screen.getByRole('button', { name: 'Reject suggestion for last_name' }))
    expect(state(store, 'm_last')).toBe('rejected')
    expect(store.getState().stats().suggested).toBe(before - 2)
    expect(screen.getByText('All suggestions reviewed')).toBeInTheDocument()
  })

  it('AC4: bulk accept threshold and undo', async () => {
    const { store } = renderWorkbench(<SuggestionReview onMapRemaining={() => {}} />, twoSuggestions())
    await userEvent.click(screen.getByRole('button', { name: 'Accept all ≥ 75%' }))
    expect(state(store, 'm_email')).toBe('active')
    expect(state(store, 'm_last')).toBe('pending')
    expect(await screen.findByText('Accepted 1 suggestion')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Undo' }))
    expect(state(store, 'm_email')).toBe('pending')
    await userEvent.click(screen.getByRole('button', { name: 'Accept all' }))
    expect([state(store, 'm_email'), state(store, 'm_last')]).toEqual(['active', 'active'])
  })

  it('AC4: keyboard J/K/A/R', async () => {
    const { store } = renderWorkbench(<SuggestionReview onMapRemaining={() => {}} />, twoSuggestions())
    screen.getByRole('list', { name: 'Suggestions' }).focus()
    await userEvent.keyboard('jkj')
    await userEvent.keyboard('r')
    expect(state(store, 'm_last')).toBe('rejected')
    await userEvent.keyboard('a')
    expect(state(store, 'm_email')).toBe('active')
  })

  it('offers to map the remaining columns once all are reviewed', async () => {
    const onMapRemaining = vi.fn()
    renderWorkbench(<SuggestionReview onMapRemaining={onMapRemaining} />)
    await userEvent.click(screen.getByRole('button', { name: 'Accept suggestion for email_address' }))
    await userEvent.click(screen.getByRole('button', { name: /Map the remaining \d+ columns/ }))
    expect(onMapRemaining).toHaveBeenCalledOnce()
  })
})
