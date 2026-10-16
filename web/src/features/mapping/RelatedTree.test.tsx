import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { server, setupMswServer } from '@/test/msw'
import { embedView, ordersGraph, ordersView, renderWorkbench } from '@/test/mapping'
import { mockSession } from '@/test/session'
import { RelatedTree } from './RelatedTree'

setupMswServer()
beforeEach(() => mockSession('owner'))

describe('RelatedTree', () => {
  it('AC1: lists parents and children with counts', async () => {
    renderWorkbench(<RelatedTree />, embedView())
    const list = screen.getByRole('region', { name: 'Related tables' })
    const items = within(list).getAllByRole('listitem')
    expect(items.map((i) => i.textContent)).toEqual([
      expect.stringContaining('↗ address via address_id'),
      expect.stringContaining('↙ orders via customer_id'),
      expect.stringContaining('↙ page_view via customer_id'),
    ])
    expect(items[0]).toHaveTextContent('~400 rows')
    expect(items[1]).toHaveTextContent('~600 per row')
    expect(within(items[1]!).getByText('Embedded')).toBeInTheDocument()
    // More child rows per parent than the default limit: a warning with the numbers.
    expect(within(items[2]!).getByLabelText('About 2,500 rows per document, more than the limit of 1000: arrays will be truncated.')).toBeInTheDocument()
    expect(await within(items[0]!).findByRole('button', { name: 'Embed' })).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: /Related tables/ }))
    expect(within(list).queryAllByRole('listitem')).toHaveLength(0)
  })

  it('says when there are no related tables', () => {
    renderWorkbench(<RelatedTree />, embedView({ embedCandidates: [] }))
    expect(screen.getByText('No foreign keys or virtual relationships to or from this table.')).toBeInTheDocument()
  })

  it('AC6: hops, junctions and virtual badge', async () => {
    server.use(http.get('*/api/v1/connections/:id/relations', () => HttpResponse.json(ordersGraph())))
    renderWorkbench(<RelatedTree />, ordersView())
    const items = await screen.findByTestId('related-order_items')
    expect(items).toHaveTextContent('↙ order_items via order_id')
    // No index on order_items.order_id: the advice is on the row.
    expect(within(items).getByLabelText(/Lookups on order_items.order_id will scan the table/)).toBeInTheDocument()
    expect(screen.getAllByTestId('related-customers').map((r) => r.textContent)).toEqual([
      expect.stringContaining('↗ customers via customer_id'),
      expect.stringContaining('virtual'),
    ])
    // order_items expands to its own relations: order_items → products.
    expect(screen.queryByTestId('related-order_items/products')).not.toBeInTheDocument()
    await userEvent.click(within(items).getByRole('button', { name: 'Show tables related to order_items' }))
    expect(screen.getByTestId('related-order_items/products')).toHaveTextContent('↗ products via product_id')
    // The junction candidate.
    const junction = screen.getByTestId('junction-order_items-products')
    expect(junction).toHaveTextContent('↔ products via order_items (quantity, unit_price)')
    expect(within(junction).getByRole('button', { name: 'Embed as array of products' })).toBeInTheDocument()
  })

  it('an embedded table opens to show what it references, so that can be embedded next', async () => {
    server.use(http.get('*/api/v1/connections/:id/relations', () => HttpResponse.json(ordersGraph())))
    const view = ordersView()
    view.spec = { ...view.spec, relations: [{ id: 'emb_i', kind: 'one-to-many', table: { schema: 'public', name: 'order_items' }, join: [{ parent: 'order_id', child: 'order_id' }] }] }
    renderWorkbench(<RelatedTree />, view)
    const items = await screen.findByTestId('related-order_items')
    expect(within(items).getAllByText('Embedded')[0]).toBeInTheDocument()
    expect(await screen.findByTestId('related-order_items/products')).toHaveTextContent('↗ products via product_id')
    await userEvent.click(within(items).getByRole('button', { name: 'Hide tables related to order_items' }))
    expect(screen.queryByTestId('related-order_items/products')).not.toBeInTheDocument()
  })

  it('OC-069 AC1: per-row relations nest under the group\'s rows', async () => {
    // Order Details grouped by order_id: the lines are items[]; product_id differs per line.
    const view = embedView({
      embedCandidates: [
        { kind: 'many-to-one', table: { schema: 'shop', name: 'product' }, join: [{ parent: 'product_id', child: 'product_id' }], constraint: 'od_product_fk', rowEstimate: 50 },
        { kind: 'many-to-one', table: { schema: 'shop', name: 'orders' }, join: [{ parent: 'order_id', child: 'order_id' }], constraint: 'od_orders_fk', rowEstimate: 600 },
      ],
    })
    const table = { schema: 'shop', name: 'Order Details' }
    view.spec = {
      ...view.spec,
      mode: 'composed',
      root: { table, groupBy: ['order_id'] },
      docSchema: { ...view.spec.docSchema, root: { ...view.spec.docSchema.root, children: [{ id: 'f_items', name: 'items', type: 'array', items: { id: 'f_line', name: '[]', type: 'object', children: [] } }] } },
      relations: [{ id: 'emb_lines', parentId: null, group: true, kind: 'one-to-many', table, join: [{ parent: 'order_id', child: 'order_id' }], targetFieldId: 'f_items' }],
      fieldMappings: [],
    }
    server.use(http.get('*/api/v1/connections/:id/schema/:schema/:table', () => HttpResponse.json({ name: 'product', columns: [] })))
    renderWorkbench(<RelatedTree />, view)
    const product = screen.getByTestId('related-product')
    await waitFor(() => expect(within(product).getByRole('button', { name: 'Embed' })).toBeEnabled())
    await userEvent.click(within(product).getByRole('button', { name: 'Embed' }))
    expect(await screen.findByRole('dialog')).toHaveTextContent('They go inside the items field.')
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    // Joined on the group-by column: one value per document, so it stays on the root.
    await userEvent.click(within(screen.getByTestId('related-orders')).getByRole('button', { name: 'Embed' }))
    expect(await screen.findByRole('dialog')).not.toHaveTextContent('They go inside')
  })

  it('OC-069 AC1: a product already in the group\'s rows shows as embedded', () => {
    const view = embedView({
      embedCandidates: [{ kind: 'many-to-one', table: { schema: 'shop', name: 'product' }, join: [{ parent: 'product_id', child: 'product_id' }], constraint: 'od_product_fk' }],
    })
    const table = { schema: 'shop', name: 'Order Details' }
    view.spec = {
      ...view.spec,
      mode: 'composed',
      root: { table, groupBy: ['order_id'] },
      relations: [
        { id: 'emb_lines', parentId: null, group: true, kind: 'one-to-many', table, join: [{ parent: 'order_id', child: 'order_id' }], targetFieldId: 'f_orders' },
        { id: 'emb_p', parentId: 'emb_lines', kind: 'many-to-one', lookupOnly: true, table: { schema: 'shop', name: 'product' }, join: [{ parent: 'product_id', child: 'product_id' }] },
      ],
    }
    renderWorkbench(<RelatedTree />, view)
    expect(within(screen.getByTestId('related-product')).getByText('Embedded')).toBeInTheDocument()
  })
})
