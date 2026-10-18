import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { composedView, mockComposer, ordersColumns } from '@/test/composer'
import { renderWorkbench } from '@/test/mapping'
import { setupMswServer } from '@/test/msw'
import { mockSession } from '@/test/session'
import { ComposerInspector } from './ComposerInspector'

setupMswServer()
beforeEach(() => {
  mockSession('owner')
  mockComposer()
})

const groups = (select: HTMLElement) =>
  Object.fromEntries([...select.querySelectorAll('optgroup')].map((g) => [g.label, [...g.querySelectorAll('option')].map((o) => o.textContent!.split(' · ')[0])]))

describe('ComposerInspector', () => {
  it('AC4: lists in-scope columns only', async () => {
    const { store } = renderWorkbench(<ComposerInspector focusSource={0} onBindTable={() => {}} />, composedView(), ordersColumns())
    store.getState().select({ kind: 'field', id: 'f_sku' })
    const source = await screen.findByRole('combobox', { name: 'Source for sku' })
    await screen.findByRole('option', { name: /^sku/ })
    const g = groups(source)
    expect(Object.keys(g)).toEqual(['orders (driving table)', 'order_items', 'products (lookup)'])
    expect(g['orders (driving table)']).toEqual(['order_id', 'order_no', 'customer_id', 'cust_code'])
    expect(g['order_items']).toContain('line_no')
    expect(g['products (lookup)']).toEqual(['product_id', 'sku', 'name'])
    expect(within(source).queryByRole('option', { name: /^label/ })).not.toBeInTheDocument() // tags is a sibling
    expect(within(source).queryByRole('option', { name: /^tagged_at/ })).not.toBeInTheDocument()
    expect(source).toHaveValue('rel_plookup|sku|')

    // Picking a column maps it (replacing the source, keeping the transform).
    await userEvent.selectOptions(source, 'rel_items|product_id|')
    const m = store.getState().history.present.fieldMappings.find((x) => x.targetFieldId === 'f_sku' && x.state === 'active')!
    expect(m.sources).toEqual([{ column: 'product_id', relation: 'rel_items' }])
  })

  it('a bound array shows its binding; the junction payload is in scope of its items', async () => {
    const { store } = renderWorkbench(<ComposerInspector focusSource={0} onBindTable={() => {}} />, composedView(), ordersColumns())
    store.getState().select({ kind: 'field', id: 'f_tags' })
    expect(await screen.findByText('via junction order_tags')).toBeInTheDocument()
    store.getState().select({ kind: 'field', id: 'f_label' })
    const source = await screen.findByRole('combobox', { name: 'Source for label' })
    expect(within(source).getByRole('option', { name: /tagged_at \(junction\)/ })).toBeInTheDocument()
  })

  it('offers the names an expression can use', async () => {
    const { store } = renderWorkbench(<ComposerInspector focusSource={0} onBindTable={() => {}} />, composedView(), ordersColumns())
    store.getState().select({ kind: 'field', id: 'f_line' })
    const names = await screen.findByRole('group', { name: 'Names in scope' })
    await userEvent.click(within(names).getByRole('button', { name: '$root' }))
    await userEvent.type(screen.getByRole('textbox', { name: 'Expression' }), '.order_no')
    await userEvent.click(screen.getByRole('button', { name: 'Use expression' }))
    const m = store.getState().history.present.fieldMappings.find((x) => x.targetFieldId === 'f_line' && x.state === 'active')!
    expect(m.transforms).toEqual([{ op: 'expression', args: { expr: '$root.order_no' } }])
  })

  it('OC-178 AC3: a field nothing fills can be marked not populated', async () => {
    const { store } = renderWorkbench(<ComposerInspector focusSource={0} onBindTable={() => {}} />, composedView(), ordersColumns())
    store.getState().apply((s) => ({ ...s, fieldMappings: s.fieldMappings.filter((m) => m.targetFieldId !== 'f_line') }))
    store.getState().select({ kind: 'field', id: 'f_line' })
    // Editors only: enabled once the session is known.
    await waitFor(() => expect(screen.getByRole('button', { name: 'Mark not populated' })).toBeEnabled())
    await userEvent.click(screen.getByRole('button', { name: 'Mark not populated' }))
    expect(store.getState().history.present.fieldMappings.filter((m) => m.targetFieldId === 'f_line').map((m) => m.state)).toEqual(['notPopulated'])
    expect(screen.getByRole('note')).toHaveTextContent('leaves this field empty on purpose')
    expect(screen.queryByRole('button', { name: 'Mark not populated' })).toBeNull()
    store.getState().undo()
    expect(await screen.findByRole('button', { name: 'Mark not populated' })).toBeInTheDocument()
    // A mapped field doesn't offer it.
    store.getState().select({ kind: 'field', id: 'f_sku' })
    await screen.findByRole('combobox', { name: 'Source for sku' })
    expect(screen.queryByRole('button', { name: 'Mark not populated' })).toBeNull()
  })

  it('OC-179 AC1: Now in the Composer, with the format from the field\'s type', async () => {
    const { store } = renderWorkbench(<ComposerInspector focusSource={0} onBindTable={() => {}} />, composedView(), ordersColumns())
    store.getState().select({ kind: 'field', id: 'f_line' })
    await userEvent.click(await screen.findByRole('radio', { name: 'Date' }))
    // It's mapped from line_no: leaving the source asks first.
    await userEvent.click(await screen.findByRole('button', { name: 'Remove the mapping' }))
    // lineNo is an integer and the document has no other dates: epoch ms.
    expect(within(screen.getByRole('combobox', { name: 'Format' })).getByRole('option', { selected: true })).toHaveTextContent('Epoch milliseconds')
    expect(screen.getByTestId('now-sample').textContent).toMatch(/^\d{13}$/)
    expect(screen.queryByRole('combobox', { name: 'Source for lineNo' })).toBeNull()
  })
})
