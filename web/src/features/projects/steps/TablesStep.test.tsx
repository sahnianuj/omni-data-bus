import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { CreateMappingRequest } from '@/features/mapping/api'
import type { MappingSummary } from '@/features/mapping/types'
import { mockSchema, shopSnapshot } from '@/test/discovery'
import { mockKeyspaces } from '@/test/keyspaces'
import { stats } from '@/test/mapping'
import { server, setupMswServer } from '@/test/msw'
import { makeProject, mockProject, repo } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'

setupMswServer()

const summary = (table: string, collection: string): MappingSummary => ({
  id: `m-${table}`, sourceSchema: 'shop', sourceTable: table, target: { bucket: 'travel-sample', scope: 'inventory', collection },
  status: 'draft', stats: stats(), automapStats: stats(), version: 1, updatedAt: new Date().toISOString(),
})

beforeEach(() => {
  mockSession('owner')
  mockProject(makeProject({ source: repo('postgres'), target: repo('couchbase') }))
  mockSchema(shopSnapshot())
  mockKeyspaces()
})

/** Serves the mapping list (starting with `initial`) and records bulk-create bodies. */
function mappingsServer(initial: MappingSummary[]) {
  const state = { items: initial, bulk: [] as CreateMappingRequest[][] }
  server.use(
    http.get('*/api/v1/projects/:pid/mappings', () => HttpResponse.json({ items: state.items })),
    http.post('*/api/v1/projects/:pid/mappings:bulk', async ({ request }) => {
      const { items } = (await request.json()) as { items: CreateMappingRequest[] }
      state.bulk.push(items)
      const results = items.map((i) => ({ sourceSchema: i.sourceSchema, sourceTable: i.sourceTable, mapping: summary(i.sourceTable, i.target.collection) }))
      state.items = [...state.items, ...results.map((r) => r.mapping)]
      return HttpResponse.json({ results })
    }),
  )
  return state
}

describe('TablesStep', () => {
  it('AC1: per-row target collection editable', async () => {
    const state = mappingsServer([summary('address', 'address')])
    const { router } = renderRoutes(routes, '/projects/p1/tables')

    // The already-mapped table is checked, locked and links to its workbench.
    expect(await screen.findByRole('checkbox', { name: 'Select shop.address' })).toBeDisabled()
    expect(screen.getByRole('link', { name: 'Mapped to travel-sample.inventory.address' })).toHaveAttribute('href', '/projects/p1/mappings/m-address')

    await userEvent.click(screen.getByRole('checkbox', { name: 'Select shop.customer' }))
    await userEvent.click(screen.getByRole('checkbox', { name: 'Select shop.Order Details' }))

    // Defaults come from the sanitized table name; each row can be renamed.
    expect(screen.getByRole('textbox', { name: 'Collection for shop.Order Details' })).toHaveValue('order_details')
    const customer = screen.getByRole('textbox', { name: 'Collection for shop.customer' })
    expect(customer).toHaveValue('customer')
    await userEvent.clear(customer)
    await userEvent.type(customer, 'bad name')
    expect(screen.getByRole('button', { name: 'Create 2 mappings' })).toBeDisabled()
    await userEvent.clear(customer)
    await userEvent.type(customer, 'customers')

    await userEvent.click(screen.getByRole('button', { name: 'Create 2 mappings' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/projects/p1/mappings'))
    expect(state.bulk).toEqual([
      [
        { sourceSchema: 'shop', sourceTable: 'customer', target: { bucket: 'travel-sample', scope: 'inventory', collection: 'customers', createIfMissing: true } },
        { sourceSchema: 'shop', sourceTable: 'Order Details', target: { bucket: 'travel-sample', scope: 'inventory', collection: 'order_details', createIfMissing: true } },
      ],
    ])
  })

  it('a table chosen in the schema explorer arrives preselected', async () => {
    mappingsServer([summary('address', 'address')])
    renderRoutes(routes, `/projects/p1/tables?select=${encodeURIComponent('shop.orders')}`)
    expect(await screen.findByRole('checkbox', { name: 'Select shop.orders' })).toBeChecked()
    expect(screen.getByRole('textbox', { name: 'Collection for shop.orders' })).toHaveValue('orders')
    expect(screen.getByText('1 table selected')).toBeInTheDocument()
  })
})
