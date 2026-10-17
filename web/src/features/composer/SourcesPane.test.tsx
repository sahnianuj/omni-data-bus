import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { composedView, mockComposer } from '@/test/composer'
import { mockMapping } from '@/test/mapping'
import { server, setupMswServer } from '@/test/msw'
import { makeProject, mockProject, repo } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'

setupMswServer()

const ref = (name: string) => ({ schema: 'public', name })
const hop = (from: string, to: string, kind: 'many-to-one' | 'one-to-many', join: [string, string]) => ({ from: ref(from), to: ref(to), kind, join: [{ parent: join[0], child: join[1] }], indexed: true })

// products → brands (N:1), reviews → products (1:N), and the way back to order_items.
function mockHops(maxRelationDepth = 4) {
  const hops: Record<string, unknown[]> = {
    orders: [hop('orders', 'customers', 'many-to-one', ['customer_id', 'customer_id']), hop('orders', 'order_items', 'one-to-many', ['order_id', 'order_id'])],
    order_items: [hop('order_items', 'orders', 'many-to-one', ['order_id', 'order_id']), hop('order_items', 'products', 'many-to-one', ['product_id', 'product_id'])],
    customers: [hop('customers', 'regions', 'many-to-one', ['region_id', 'region_id'])],
    products: [hop('products', 'brands', 'many-to-one', ['brand_id', 'brand_id']), hop('products', 'reviews', 'one-to-many', ['product_id', 'product_id']), hop('products', 'order_items', 'one-to-many', ['product_id', 'product_id'])],
  }
  server.use(
    http.get('*/api/v1/connections/:id/relations/hops', ({ request }) => {
      const [schema = '', name = ''] = (new URL(request.url).searchParams.get('table') ?? '').split('.')
      return HttpResponse.json({ table: { schema, name }, hops: hops[name] ?? [], junctions: [], advice: [], maxRelationDepth })
    }),
    http.get('*/api/v1/connections/:id/schema/public/brands', () =>
      HttpResponse.json({
        schema: 'public', name: 'brands', kind: 'table', rowEstimate: 5, primaryKey: ['brand_id'], foreignKeys: [], indexes: [],
        columns: [
          { name: 'brand_id', ordinal: 1, dataType: 'integer', udtName: 'int4', jsonType: 'integer', nullable: false, isPrimaryKey: true },
          { name: 'label', ordinal: 2, dataType: 'text', udtName: 'text', jsonType: 'string', nullable: true },
        ],
      }),
    ),
  )
}

beforeEach(() => {
  window.localStorage.clear()
  mockSession('owner')
  mockProject(makeProject({ source: repo('postgres'), target: repo('couchbase'), mappings: 1, readyMappings: 0 }))
  mockComposer()
  server.use(http.get('*/api/v1/projects/:pid/mappings', () => HttpResponse.json({ items: [] })))
})

const section = () => screen.findByRole('region', { name: 'In this document' })

describe('SourcesPane › In this document', () => {
  it('OC-142 AC1: an embedded object’s relationships appear under it', async () => {
    mockHops()
    mockMapping(composedView())
    renderRoutes(routes, '/projects/p1/mappings/m1')
    const doc = await section()
    // order_items → items[], products → items[].product nested under it.
    expect(within(doc).getByRole('button', { name: '→ items[]' })).toBeInTheDocument()
    expect(within(doc).getByRole('button', { name: '→ items[].product' })).toBeInTheDocument()
    expect(within(doc).getByText('lookup')).toBeInTheDocument()
    const refs = await within(doc).findByRole('group', { name: 'Tables products references' })
    expect(within(refs).getByRole('button', { name: 'Embed brands into items[].product' })).toBeEnabled()
    expect(within(refs).getByText('N:1')).toBeInTheDocument()
    const children = within(doc).getAllByRole('group').find((g) => g.getAttribute('aria-label')?.includes('products') && g !== refs)!
    expect(within(children).getByText('1:N')).toBeInTheDocument()
    expect(within(children).getByRole('button', { name: 'Embed reviews into items[].product' })).toBeInTheDocument()
    // Not the way back to order_items, which embedded the product; no Look up on 1:N.
    expect(within(children).queryByRole('button', { name: /order_items/ })).not.toBeInTheDocument()
    expect(within(children).queryByRole('button', { name: /Look up/ })).not.toBeInTheDocument()
  })

  it('OC-142 AC2: Embed adds a bound object under the instance in one undo step; Look up adds no node', async () => {
    mockHops()
    const state = mockMapping(composedView())
    renderRoutes(routes, '/projects/p1/mappings/m1')
    const doc = await section()
    await userEvent.click(await within(doc).findByRole('button', { name: 'Embed brands into items[].product' }))
    const dialog = await screen.findByRole('dialog')
    expect(await within(dialog).findByRole('radio', { name: 'Many-to-one: brand_id = brand_id' })).toBeChecked()
    await userEvent.click(within(dialog).getByRole('checkbox', { name: 'Auto-map' }))
    await userEvent.click(within(dialog).getByRole('button', { name: 'Bind' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

    const added = await within(doc).findByRole('button', { name: '→ items[].product.brand' })
    expect(added).toBeInTheDocument()
    await waitFor(() => {
      const rel = (state.view.spec.relations ?? []).find((r) => r.table.name === 'brands')
      expect(rel).toMatchObject({ kind: 'many-to-one', parentId: 'rel_prod', join: [{ parent: 'brand_id', child: 'brand_id' }] })
    })
    // One undo removes the node and its relation.
    await userEvent.click(screen.getByRole('button', { name: 'Undo' }))
    await waitFor(() => expect(within(doc).queryByRole('button', { name: '→ items[].product.brand' })).not.toBeInTheDocument())
    expect(within(doc).getByRole('button', { name: 'Embed brands into items[].product' })).toBeInTheDocument()

    // Cancelling the dialog leaves nothing behind.
    await userEvent.click(within(doc).getByRole('button', { name: 'Embed brands into items[].product' }))
    await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(within(screen.getByRole('tree', { name: 'Document' })).queryByRole('treeitem', { name: /brand/ })).not.toBeInTheDocument()

    // Look up: a lookup-only relation in the product's scope.
    await userEvent.click(within(doc).getByRole('button', { name: 'Look up brands in items[].product' }))
    const lookup = await screen.findByRole('dialog')
    await within(lookup).findByRole('radio', { name: 'Many-to-one: brand_id = brand_id' })
    await userEvent.click(within(lookup).getByRole('button', { name: /Add|Bind/ }))
    await waitFor(() => expect(state.view.spec.relations.find((r) => r.table.name === 'brands')).toMatchObject({ lookupOnly: true, parentId: 'rel_prod' }))
    expect(within(screen.getByRole('tree', { name: 'Document' })).queryByRole('treeitem', { name: /brand/ })).not.toBeInTheDocument()
    expect(within(doc).getAllByText('lookup')).toHaveLength(2)
  })

  it('OC-142 AC3: at the depth limit the actions are disabled and say why', async () => {
    mockHops(2)
    mockMapping(composedView())
    renderRoutes(routes, '/projects/p1/mappings/m1')
    const doc = await section()
    const embed = await within(doc).findByRole('button', { name: 'Embed brands into items[].product' })
    expect(embed).toBeDisabled()
    expect(embed).toHaveAttribute('title', 'Deepest level reached (2 of 2). An administrator can raise OC_MAX_RELATION_DEPTH up to 6.')
    expect(within(doc).getByRole('button', { name: 'Look up brands in items[].product' })).toBeDisabled()
    // One level up is still open.
    expect(within(doc).getByRole('button', { name: 'Embed regions into customer' })).toBeEnabled()
  })
})
