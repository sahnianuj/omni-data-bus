import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { pathsById } from '@/features/docschema/docschemaOps'
import { renderWorkbench } from '@/test/mapping'
import { UnmappedPanel } from './UnmappedPanel'

describe('UnmappedPanel', () => {
  it('AC6: create field and map', async () => {
    const { store } = renderWorkbench(<UnmappedPanel />)
    const sources = screen.getByRole('region', { name: 'Unmapped source columns' })
    expect(within(sources).getAllByRole('listitem').map((li) => li.firstChild?.textContent)).toEqual(['last_name', 'loyalty_tier', 'age_text'])
    await userEvent.click(screen.getByRole('button', { name: 'Create a field for loyalty_tier' }))
    const spec = store.getState().history.present
    const m = spec.fieldMappings.find((x) => x.sources[0]?.column === 'loyalty_tier')!
    expect(m).toMatchObject({ state: 'active', origin: 'manual' })
    expect(pathsById(spec.docSchema).get(m.targetFieldId)).toBe('loyaltyTier')
    expect(within(sources).queryByText('loyalty_tier')).not.toBeInTheDocument()
  })

  it('AC6: ignore updates stats', async () => {
    const { store } = renderWorkbench(<UnmappedPanel />)
    const before = store.getState().stats()
    await userEvent.click(screen.getByRole('button', { name: 'Ignore age_text' }))
    const after = store.getState().stats()
    expect(after.ignored).toBe(before.ignored + 1)
    expect(after.unmappedSource).toBe(before.unmappedSource - 1)
  })

  it('AC6: create for all unmapped', async () => {
    const { store } = renderWorkbench(<UnmappedPanel />)
    await userEvent.click(screen.getByRole('button', { name: 'Create fields for all' }))
    expect(store.getState().stats().unmappedSource).toBe(0)
    expect(store.getState().history.past).toHaveLength(1) // one undoable edit
    expect(within(screen.getByRole('region', { name: 'Unmapped source columns' })).getByText('Everything is mapped.')).toBeInTheDocument()
  })

  it('selecting an unmapped target opens it in the inspector', async () => {
    const { store } = renderWorkbench(<UnmappedPanel />)
    const targets = screen.getByRole('region', { name: 'Unmapped target fields' })
    await userEvent.click(within(targets).getByRole('button', { name: /^age/ }))
    expect(store.getState().selection).toEqual({ kind: 'field', id: 'f_age' })
  })
})
