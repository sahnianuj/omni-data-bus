import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { withFixedLayout } from '@/test/layout'
import { server, setupMswServer } from '@/test/msw'
import { embedView, mappingView, ordersGraph, ordersView, renderWorkbench, scol } from '@/test/mapping'
import { mockSession } from '@/test/session'
import type { AddEmbedRequest } from './api'
import { MappingCanvas } from './MappingCanvas'
import { RelatedTree } from './RelatedTree'

setupMswServer()
withFixedLayout(320, 1200)
beforeEach(() => mockSession('owner'))

/** The n-th Embed button, once the session enabled it (editor-only). */
async function embedButton(n: number) {
  await waitFor(() => expect(screen.getAllByRole('button', { name: 'Embed' })[n]).toBeEnabled())
  return screen.getAllByRole('button', { name: 'Embed' })[n]!
}

describe('EmbedDialog', () => {
  it('AC2: creates embed and field', async () => {
    const bodies: { ifMatch: string | null; body: AddEmbedRequest }[] = []
    server.use(
      http.get('*/api/v1/connections/:id/schema/:schema/:table', ({ params }) => {
        expect(params.id).toBe('p1::source')
        return HttpResponse.json({ name: 'page_view', columns: [scol('view_id', 'integer'), scol('customer_id', 'integer'), scol('viewed_at', 'datetime')] })
      }),
      http.post('*/api/v1/projects/:pid/mappings/:mid/embeds', async ({ request }) => {
        const body = (await request.json()) as AddEmbedRequest
        bodies.push({ ifMatch: request.headers.get('If-Match'), body })
        // The server adds the field, the embed and the auto-mapped items.
        const next = embedView()
        next.spec.docSchema.root.children!.push({ id: 'f_views', name: 'views', type: 'array', items: { id: 'f_vi', name: '[]', type: 'object', children: [{ id: 'f_at', name: 'viewedAt', type: 'datetime' }] } })
        next.spec.relations.push({ id: 'emb_pv', parentId: null, kind: 'one-to-many', table: { schema: 'shop', name: 'page_view' }, join: [{ parent: 'customer_id', child: 'customer_id' }], targetFieldId: 'f_views', limit: 50 })
        next.spec.fieldMappings.push({ id: 'm_at', targetFieldId: 'f_at', sources: [{ relation: 'emb_pv', column: 'viewed_at' }], transforms: [], origin: 'auto', state: 'active' })
        next.embedTables!.emb_pv = { table: { schema: 'shop', name: 'page_view' }, columns: [scol('view_id', 'integer'), scol('customer_id', 'integer'), scol('viewed_at', 'datetime')] }
        return HttpResponse.json({ ...next, version: 4 })
      }),
    )
    const { store } = renderWorkbench(<MappingCanvas />, embedView())
    await userEvent.click(await embedButton(1))
    const dialog = await screen.findByRole('dialog', { name: 'Embed page_view' })
    expect(dialog).toHaveTextContent('Array of objects (one-to-many)')
    const name = screen.getByLabelText('Field name')
    expect(name).toHaveValue('pageView')
    await userEvent.clear(name)
    await userEvent.type(name, 'views')
    await waitFor(() => expect(within(screen.getByLabelText('Order items by')).getByRole('option', { name: 'viewed_at' })).toBeInTheDocument())
    await userEvent.selectOptions(screen.getByLabelText('Order items by'), 'viewed_at')
    await userEvent.click(screen.getByLabelText('Descending'))
    await userEvent.clear(screen.getByLabelText('At most'))
    await userEvent.type(screen.getByLabelText('At most'), '50')
    expect(screen.getByLabelText(/Generate fields from table/)).toBeChecked()
    await userEvent.click(screen.getByRole('button', { name: 'Embed' }))

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(bodies).toEqual([{
      ifMatch: '"3"',
      body: {
        kind: 'one-to-many', table: { schema: 'shop', name: 'page_view' }, join: [{ parent: 'customer_id', child: 'customer_id' }], targetName: 'views',
        generate: true, limit: 50, empty: 'array', orderBy: [{ column: 'viewed_at', desc: true }],
      },
    }])
    // The new field and the embedded group appear.
    expect(store.getState().version).toBe(4)
    expect(store.getState().history.present.relations.map((e) => e.id)).toEqual(['emb_o', 'emb_pv'])
    expect(screen.getByTestId('embed-group-emb_pv')).toHaveTextContent('page_view → views[] (array)')
    expect(screen.getByTestId('source-emb_pv:viewed_at')).toBeInTheDocument()
    expect(screen.getByTestId('target-views[].viewedAt')).toBeInTheDocument()
  })

  it('many-to-one has no array options; errors stay in the dialog', async () => {
    server.use(
      http.get('*/api/v1/connections/:id/schema/:schema/:table', () => HttpResponse.json({ name: 'address', columns: [] })),
      http.post('*/api/v1/projects/:pid/mappings/:mid/embeds', () =>
        HttpResponse.json({ type: 'about:blank', title: "Can't embed", status: 422, code: 'VALIDATION_FAILED', detail: 'Column address_id isn\'t in customer.' }, { status: 422 }),
      ),
    )
    renderWorkbench(<MappingCanvas />, embedView({ ...mappingView(), embedCandidates: embedView().embedCandidates, embedTables: {} }))
    await userEvent.click(await embedButton(0))
    await screen.findByRole('dialog', { name: 'Embed address' })
    expect(screen.queryByLabelText('At most')).not.toBeInTheDocument()
    expect(screen.getByText('Object (many-to-one)')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Embed' }))
    expect(await screen.findByText("Column address_id isn't in customer.")).toBeInTheDocument()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('AC7: embeds a two-hop path', async () => {
    const bodies: AddEmbedRequest[] = []
    server.use(
      http.get('*/api/v1/connections/:id/relations', () => HttpResponse.json(ordersGraph())),
      http.get('*/api/v1/connections/:id/schema/:schema/:table', ({ params }) => HttpResponse.json({ name: params.table, columns: [scol('product_id', 'integer'), scol('sku', 'string')] })),
      http.post('*/api/v1/projects/:pid/mappings/:mid/embeds', async ({ request }) => {
        bodies.push((await request.json()) as AddEmbedRequest)
        return HttpResponse.json({ ...ordersView(), version: 4 })
      }),
    )
    renderWorkbench(<RelatedTree />, ordersView())
    await userEvent.click(await screen.findByRole('button', { name: 'Show tables related to order_items' }))
    const row = screen.getByTestId('related-order_items/products')
    await waitFor(() => expect(within(row).getByRole('button', { name: 'Embed' })).toBeEnabled())
    await userEvent.click(within(row).getByRole('button', { name: 'Embed' }))
    const dialog = await screen.findByRole('dialog', { name: 'Embed products' })
    expect(within(dialog).getByTestId('embed-path')).toHaveTextContent('↙ order_items → ↗ products')
    expect(within(dialog).getByLabelText('Field name for order_items')).toHaveValue('orderItems')
    await userEvent.clear(within(dialog).getByLabelText('Field name for order_items'))
    await userEvent.type(within(dialog).getByLabelText('Field name for order_items'), 'items')
    expect(within(dialog).getByLabelText('Field name for products')).toHaveValue('products')
    await userEvent.clear(within(dialog).getByLabelText('Field name for products'))
    await userEvent.type(within(dialog).getByLabelText('Field name for products'), 'product')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Embed' }))
    await waitFor(() => expect(bodies).toHaveLength(1))
    expect(bodies[0]).toEqual({
      kind: 'one-to-many', table: { schema: 'public', name: 'order_items' }, join: [{ parent: 'order_id', child: 'order_id' }], targetName: 'items', generate: true,
      then: [{ kind: 'many-to-one', table: { schema: 'public', name: 'products' }, join: [{ parent: 'product_id', child: 'product_id' }], targetName: 'product', generate: true }],
    })
  })

  it('AC4: junction prefill and payload columns', async () => {
    const bodies: AddEmbedRequest[] = []
    server.use(
      http.get('*/api/v1/connections/:id/relations', () => HttpResponse.json(ordersGraph())),
      http.get('*/api/v1/connections/:id/schema/:schema/:table', ({ params }) =>
        HttpResponse.json(params.table === 'order_items'
          ? { name: 'order_items', columns: [scol('order_id', 'integer'), scol('line_no', 'integer'), scol('quantity', 'integer'), scol('unit_price', 'number')] }
          : { name: 'products', columns: [scol('product_id', 'integer'), scol('sku', 'string')] })),
      http.post('*/api/v1/projects/:pid/mappings/:mid/embeds', async ({ request }) => {
        bodies.push((await request.json()) as AddEmbedRequest)
        return HttpResponse.json({ ...ordersView(), version: 4 })
      }),
    )
    renderWorkbench(<RelatedTree />, ordersView())
    const button = await screen.findByRole('button', { name: 'Embed as array of products' })
    await waitFor(() => expect(button).toBeEnabled())
    await userEvent.click(button)
    const dialog = await screen.findByRole('dialog', { name: 'Embed products via order_items' })
    expect(dialog).toHaveTextContent('Array through a junction table (many-to-many)')
    expect(within(dialog).getByLabelText('quantity')).toBeChecked()
    expect(within(dialog).getByLabelText('unit_price')).toBeChecked()
    await waitFor(() => expect(within(within(dialog).getByLabelText('Order items by')).getByRole('option', { name: 'order_items.line_no' })).toBeInTheDocument())
    await userEvent.selectOptions(within(dialog).getByLabelText('Order items by'), 'order_items.line_no')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Embed' }))
    await waitFor(() => expect(bodies).toHaveLength(1))
    expect(bodies[0]).toEqual({
      kind: 'many-to-many', table: { schema: 'public', name: 'products' }, targetName: 'products', generate: true,
      via: { table: { schema: 'public', name: 'order_items' }, parentJoin: [{ parent: 'order_id', via: 'order_id' }], childJoin: [{ via: 'product_id', child: 'product_id' }], columns: ['quantity', 'unit_price'] },
      limit: 1000, empty: 'array', orderBy: [{ column: 'line_no', desc: false, from: 'via' }],
    })
  })

  it('AC5: shape switch converts field', async () => {
    server.use(
      http.get('*/api/v1/connections/:id/relations', () => HttpResponse.json(ordersGraph())),
      http.get('*/api/v1/connections/:id/schema/:schema/:table', () => HttpResponse.json({ name: 'order_items', columns: [scol('order_id', 'integer'), scol('product_id', 'integer'), scol('status', 'string')] })),
    )
    renderWorkbench(<RelatedTree />, ordersView())
    const row = await screen.findByTestId('related-order_items')
    await waitFor(() => expect(within(row).getByRole('button', { name: 'Embed' })).toBeEnabled())
    await userEvent.click(within(row).getByRole('button', { name: 'Embed' }))
    const dialog = await screen.findByRole('dialog', { name: 'Embed order_items' })
    expect(within(dialog).getByTestId('embed-creates')).toHaveTextContent('orderItems: array<object>')
    expect(within(dialog).queryByLabelText('Value column')).not.toBeInTheDocument()
    await userEvent.selectOptions(within(dialog).getByLabelText('Array shape'), 'scalars')
    const picker = within(dialog).getByLabelText('Value column')
    await waitFor(() => expect(within(picker).getByRole('option', { name: 'product_id' })).toBeInTheDocument())
    expect(within(dialog).getByRole('button', { name: 'Embed' })).toBeDisabled()
    await userEvent.selectOptions(picker, 'product_id')
    expect(within(dialog).getByTestId('embed-creates')).toHaveTextContent('orderItems: array<integer>')
    expect(within(dialog).getByRole('button', { name: 'Embed' })).toBeEnabled()
  })
})
