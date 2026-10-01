import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { composedView, mockComposer } from '@/test/composer'
import { renderWorkbench } from '@/test/mapping'
import { setupMswServer } from '@/test/msw'
import { mockSession } from '@/test/session'
import { BindDialog } from './BindDialog'
import { addNode, nodeOf } from './composerOps'

setupMswServer()
beforeEach(() => {
  mockSession('owner')
  mockComposer()
})

const ref = (name: string) => ({ schema: 'public', name })

describe('BindDialog', () => {
  it('AC3: prefill by path kind', async () => {
    // items under orders, products dropped on it: many-to-many via order_items, with payload columns.
    const view = composedView()
    const { unmount } = renderWorkbench(<BindDialog nodeId="f_tags" table={ref('products')} onClose={() => {}} />, view)
    expect(await screen.findByRole('radio', { name: /Many-to-many via order_items/ })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'quantity' })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'unit_price' })).toBeChecked()
    unmount()

    // customers dropped on an object: many-to-one (the FK first, then the virtual one).
    renderWorkbench(<BindDialog nodeId="f_cust" table={ref('customers')} onClose={() => {}} />, view)
    const radios = await screen.findAllByRole('radio')
    expect(radios[0]).toHaveAccessibleName('Many-to-one: customer_id = customer_id')
    expect(radios[0]).toBeChecked()
    expect(radios[1]).toHaveAccessibleName(/Many-to-one: cust_code = code/)
    expect(screen.queryByText(/takes only the first one/)).not.toBeInTheDocument()
  })

  it('AC3: a table with no path offers Define relationship…', async () => {
    renderWorkbench(<BindDialog nodeId="f_cust" table={ref('audit_log')} onClose={() => {}} />, composedView())
    expect(await screen.findByRole('alert')).toHaveTextContent('No relationship connects orders to audit_log.')
    await userEvent.click(screen.getByRole('button', { name: 'Define relationship…' }))
    expect(await screen.findByRole('dialog', { name: /relationship/i })).toBeInTheDocument()
  })

  it('binds an array to order_items: relation under the root, generated fields', async () => {
    const view = composedView()
    let closed = false
    const { store } = renderWorkbench(<BindDialog nodeId="f_new" table={ref('order_items')} onClose={() => (closed = true)} />, view)
    // A fresh array to bind.
    const { spec, id } = addNode(store.getState().history.present, 'fld_root', 'array', 'lines')
    store.getState().apply(() => ({ ...spec, docSchema: { ...spec.docSchema, root: { ...spec.docSchema.root, children: spec.docSchema.root.children!.map((c) => (c.id === id ? { ...c, id: 'f_new' } : c)) } } }))
    expect(await screen.findByRole('radio', { name: 'One-to-many: order_id = order_id' })).toBeChecked()
    await userEvent.click(screen.getByRole('checkbox', { name: 'Auto-map' }))
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Order items by' }), 'line_no')
    await userEvent.click(screen.getByRole('button', { name: 'Bind' }))
    await waitFor(() => expect(closed).toBe(true))
    const s = store.getState().history.present
    const rel = s.relations.find((r) => r.targetFieldId === 'f_new')!
    expect(rel).toMatchObject({ kind: 'one-to-many', parentId: null, table: ref('order_items'), orderBy: [{ column: 'line_no' }], limit: 1000 })
    expect(nodeOf(s, 'f_new')!.items!.children!.map((c) => c.name)).toEqual(['lineNo', 'productId', 'quantity', 'unitPrice'])
  })

  it('an object bound to one-to-many explains the first adapter', async () => {
    renderWorkbench(<BindDialog nodeId="f_cust" table={ref('order_items')} onClose={() => {}} />, composedView())
    expect(await screen.findByRole('radio', { name: 'One-to-many: order_id = order_id' })).toBeChecked()
    expect(screen.getByText(/takes only the first one/)).toBeInTheDocument()
  })
})
