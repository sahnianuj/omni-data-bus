import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import axe from 'axe-core'
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
    view.spec = { ...view.spec, relations: [{ id: 'emb_i', parentId: null, kind: 'one-to-many', table: { schema: 'public', name: 'order_items' }, join: [{ parent: 'order_id', child: 'order_id' }] }] }
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

  it('OC-157 AC1/AC3: References and Referenced by, each with cardinality chips', async () => {
    renderWorkbench(
      <RelatedTree />,
      embedView({
        embedCandidates: [
          { kind: 'many-to-one', table: { schema: 'shop', name: 'address' }, join: [{ parent: 'address_id', child: 'address_id' }], constraint: 'customer_address_fk', rowEstimate: 400, direction: 'references', cardinality: 'many-to-one' },
          { kind: 'many-to-one', table: { schema: 'shop', name: 'profile' }, join: [{ parent: 'customer_id', child: 'customer_id' }], constraint: 'profile_customer_fk', rowEstimate: 1900, rowsPerParent: 1, direction: 'referencedBy', cardinality: 'one-to-one' },
          { kind: 'one-to-many', table: { schema: 'shop', name: 'orders' }, join: [{ parent: 'customer_id', child: 'customer_id' }], constraint: 'orders_customer_fk', rowEstimate: 1_200_000, rowsPerParent: 600, embedId: 'emb_o', direction: 'referencedBy', cardinality: 'one-to-many' },
        ],
      }),
    )
    const refs = screen.getByRole('group', { name: 'References' })
    const refBy = screen.getByRole('group', { name: 'Referenced by' })
    expect(within(refs).getAllByRole('listitem').map((i) => i.textContent)).toEqual([expect.stringContaining('↗ address via address_id')])
    expect(within(refBy).getAllByRole('listitem').map((i) => i.textContent)).toEqual([expect.stringContaining('↙ profile via customer_id'), expect.stringContaining('↙ orders via customer_id')])
    expect(screen.getByRole('button', { name: /Related tables/ })).toHaveTextContent('3')

    expect(within(refs).getByLabelText('many-to-one')).toHaveTextContent('N:1')
    const [profile, orders] = within(refBy).getAllByRole('listitem')
    // A one-to-one shows no estimate, and is embedded as an object.
    expect(within(profile!).getByLabelText('one-to-one')).toHaveTextContent('1:1')
    expect(profile).not.toHaveTextContent('rows')
    expect(within(profile!).getByRole('button', { name: 'Embed' })).toHaveAttribute('title', 'At most one profile row each, embedded as an object')
    expect(within(orders!).getByLabelText('one-to-many')).toHaveTextContent('1:N')
    expect(orders).toHaveTextContent('~600 per row')

    // Groups close and open on their own.
    await userEvent.click(within(refBy).getByRole('button', { name: /Referenced by/ }))
    expect(within(refBy).queryAllByRole('listitem')).toHaveLength(0)
    expect(within(refs).getAllByRole('listitem')).toHaveLength(1)
  })

  it('OC-157 AC1: says when a group is empty', () => {
    renderWorkbench(
      <RelatedTree />,
      embedView({ embedCandidates: [{ kind: 'one-to-many', table: { schema: 'shop', name: 'orders' }, join: [{ parent: 'customer_id', child: 'customer_id' }], constraint: 'orders_customer_fk' }] }),
    )
    expect(screen.getByRole('group', { name: 'References' })).toHaveTextContent("customer doesn't reference other tables.")
  })

  it('OC-157 AC1: hops under an embedded row are grouped too, and junctions are Referenced by', async () => {
    server.use(http.get('*/api/v1/connections/:id/relations', () => HttpResponse.json(ordersGraph())))
    const view = ordersView()
    view.spec = { ...view.spec, relations: [{ id: 'emb_i', parentId: null, kind: 'one-to-many', table: { schema: 'public', name: 'order_items' }, join: [{ parent: 'order_id', child: 'order_id' }] }] }
    renderWorkbench(<RelatedTree />, view)
    await screen.findByTestId('related-order_items/products')
    const under = screen.getByRole('group', { name: 'Tables order_items references' })
    expect(within(under).getByTestId('related-order_items/products')).toHaveTextContent('↗ products via product_id')
    expect(screen.queryByRole('group', { name: 'Tables referencing order_items' })).not.toBeInTheDocument()

    const refBy = screen.getByRole('group', { name: 'Referenced by' })
    const junction = within(refBy).getByTestId('junction-order_items-products')
    expect(within(junction).getByLabelText('many-to-many')).toHaveTextContent('M:N')
    expect(within(screen.getByRole('group', { name: 'References' })).getAllByTestId('related-customers')).toHaveLength(2)
  })

  it('OC-157 AC5: a one-to-one embedded earlier as an array shows as embedded', () => {
    const view = embedView({
      embedCandidates: [{ kind: 'many-to-one', table: { schema: 'shop', name: 'profile' }, join: [{ parent: 'customer_id', child: 'customer_id' }], constraint: 'profile_customer_fk', direction: 'referencedBy', cardinality: 'one-to-one' }],
    })
    view.spec = { ...view.spec, relations: [{ id: 'emb_p', parentId: null, kind: 'one-to-many', table: { schema: 'shop', name: 'profile' }, join: [{ parent: 'customer_id', child: 'customer_id' }] }] }
    renderWorkbench(<RelatedTree />, view)
    const row = within(screen.getByRole('group', { name: 'Referenced by' })).getByRole('listitem')
    expect(within(row).getByText('Embedded')).toBeInTheDocument()
  })

  it('OC-157 AC7: the grouped panel has no axe violations', async () => {
    server.use(http.get('*/api/v1/connections/:id/relations', () => HttpResponse.json(ordersGraph())))
    renderWorkbench(<RelatedTree />, ordersView())
    await screen.findByTestId('junction-order_items-products')
    const res = await axe.run(screen.getByRole('region', { name: 'Related tables' }), { rules: { 'color-contrast': { enabled: false } } })
    expect(res.violations).toEqual([])
  })

  it('the pane fills its height, scrolls on its own and is resized from its top edge', () => {
    window.localStorage.removeItem('odb.pane.mapping.related')
    const { unmount } = renderWorkbench(<RelatedTree />, embedView())
    const pane = screen.getByRole('region', { name: 'Related tables' })
    expect(pane).toHaveStyle({ height: '320px' })
    const handle = within(pane).getByRole('separator', { name: 'Resize Related tables' })
    fireEvent.keyDown(handle, { key: 'ArrowUp' })
    expect(pane).toHaveStyle({ height: '336px' })
    // Closed, it's just its header: no height and no handle.
    fireEvent.click(within(pane).getByRole('button', { name: /Related tables/ }))
    expect(pane).not.toHaveStyle({ height: '336px' })
    expect(within(pane).queryByRole('separator')).not.toBeInTheDocument()
    unmount()
    renderWorkbench(<RelatedTree />, embedView())
    expect(screen.getByRole('region', { name: 'Related tables' })).toHaveStyle({ height: '336px' })
  })
})
