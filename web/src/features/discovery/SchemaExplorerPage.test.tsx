import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { DiscoveryStatus, Snapshot } from '@/features/discovery/api'
import { pollInterval } from '@/features/discovery/api'
import { mockProjectConnections, pgConnection } from '@/test/connections'
import { mockSchema, shopSnapshot } from '@/test/discovery'
import { mockKeyspaces } from '@/test/keyspaces'
import { withFixedLayout } from '@/test/layout'
import { server, setupMswServer } from '@/test/msw'
import { stats } from '@/test/mapping'
import { makeProject, mockProject, repo } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'

setupMswServer()
withFixedLayout()
beforeEach(() => {
  mockSession('owner')
  mockProject(makeProject({ source: repo('postgres', 'warn') }))
  pollInterval.ms = 5
})

const url = '/projects/p1/source/schema'

const done = (over: Partial<DiscoveryStatus> = {}): DiscoveryStatus => ({
  discoveryId: 'd1',
  connectionId: 'p1::source',
  status: 'completed',
  startedAt: new Date().toISOString(),
  snapshotId: 'snap-2',
  tableCount: 7,
  ...over,
})

/** Serves the schema from a mutable variable so a refresh can change it. */
function schemaServer(initial: Snapshot | null) {
  const state = { snapshot: initial, discovers: 0 }
  server.use(
    http.get('*/api/v1/connections/:id/schema', () =>
      state.snapshot
        ? HttpResponse.json(state.snapshot)
        : HttpResponse.json({ status: 404, code: 'SCHEMA_NOT_DISCOVERED', title: 'Schema not discovered yet' }, { status: 404 }),
    ),
  )
  return state
}

describe('SchemaExplorerPage', () => {
  it('AC6: refresh shows diff toast and sheet', async () => {
    const state = schemaServer(shopSnapshot())
    server.use(
      http.post('*/api/v1/connections/:id/discover', () => {
        state.discovers++
        state.snapshot = shopSnapshot({ snapshotId: 'snap-2' })
        return HttpResponse.json(
          done({ diff: { addedTables: ['shop.refund'], removedTables: ['shop.page_view'], changedTables: [{ table: 'shop.orders', addedColumns: ['notes'], removedColumns: [], typeChanged: [{ column: 'total', from: 'integer', to: 'bigint' }] }] } }),
        )
      }),
    )
    renderRoutes(routes, url)
    await userEvent.click(await screen.findByRole('button', { name: 'Refresh' }))
    expect(await screen.findByText('3 tables changed')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'View changes' }))
    const sheet = await screen.findByRole('dialog', { name: 'Schema changes' })
    expect(sheet).toHaveTextContent('shop.refund')
    expect(sheet).toHaveTextContent('shop.page_view')
    expect(sheet).toHaveTextContent('+ column notes')
    expect(sheet).toHaveTextContent('total: integer → bigint')
    expect(state.discovers).toBe(1)
  })

  it('AC6: preserves selection after refresh', async () => {
    const state = schemaServer(shopSnapshot())
    server.use(
      http.post('*/api/v1/connections/:id/discover', () => {
        state.snapshot = shopSnapshot({ snapshotId: 'snap-2' })
        return HttpResponse.json(done({ diff: { addedTables: [], removedTables: [], changedTables: [] } }))
      }),
    )
    renderRoutes(routes, `${url}?schema=shop&table=customer`)
    expect(await screen.findByRole('heading', { name: 'shop.customer' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(await screen.findByText('Schema is up to date')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'shop.customer' })).toBeInTheDocument()
  })

  it('selecting in the tree opens the table; a missing selection falls back to the picker', async () => {
    schemaServer(shopSnapshot())
    const { router } = renderRoutes(routes, `${url}?schema=shop&table=gone`)
    expect(await screen.findByText('Select a table')).toBeInTheDocument()
    expect(screen.getByText('Last discovered 3 minutes ago · 7 tables')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('treeitem', { name: /shop/ }))
    await userEvent.click(await screen.findByRole('treeitem', { name: /^orders/ }))
    expect(await screen.findByRole('heading', { name: 'shop.orders' })).toBeInTheDocument()
    expect(router.state.location.search).toBe('?schema=shop&table=orders')

    // A neighbour in the Relationships tab navigates too.
    await userEvent.click(screen.getByRole('tab', { name: 'Relationships' }))
    await userEvent.click(screen.getByRole('button', { name: 'Open shop.customer' }))
    expect(await screen.findByRole('heading', { name: 'shop.customer' })).toBeInTheDocument()
  })

  it('first visit discovers automatically for editors (polling a 202)', async () => {
    const state = schemaServer(null)
    let polls = 0
    server.use(
      http.post('*/api/v1/connections/:id/discover', () => {
        state.discovers++
        return HttpResponse.json(done({ status: 'running', snapshotId: undefined }), { status: 202 })
      }),
      http.get('*/api/v1/connections/:id/discoveries/:did', () => {
        polls++
        if (polls < 2) return HttpResponse.json(done({ status: 'running', snapshotId: undefined }))
        state.snapshot = shopSnapshot()
        return HttpResponse.json(done())
      }),
    )
    renderRoutes(routes, url)
    expect(await screen.findByText('Discovering the source schema…')).toBeInTheDocument()
    expect(await screen.findByRole('tree', { name: 'Source schema' })).toBeInTheDocument()
    expect(state.discovers).toBe(1)
    expect(polls).toBe(2)
  })

  it('a failed discovery shows the problem and can be retried', async () => {
    schemaServer(null)
    server.use(
      http.post('*/api/v1/connections/:id/discover', () =>
        HttpResponse.json({ status: 422, code: 'SOURCE_PERMISSION_DENIED', title: 'Permission denied', detail: 'The user lacks a required privilege.' }, { status: 422 }),
      ),
    )
    renderRoutes(routes, url)
    expect(await screen.findByText('Permission denied')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Discover schema' })).toBeInTheDocument()
  })

  it('viewers see the schema but cannot discover', async () => {
    mockSession('viewer')
    schemaServer(null)
    let posted = false
    server.use(http.post('*/api/v1/connections/:id/discover', () => ((posted = true), HttpResponse.json(done()))))
    renderRoutes(routes, url)
    expect(await screen.findByText('An editor needs to run schema discovery for this project.')).toBeInTheDocument()
    expect(posted).toBe(false)

  })

  it('without a source repository it points back to the source step', async () => {
    mockProject(makeProject({ source: null }))
    renderRoutes(routes, url)
    expect(await screen.findByText('No source repository yet')).toBeInTheDocument()
  })

  it('search and filter chips narrow the tree', async () => {
    schemaServer(shopSnapshot())
    renderRoutes(routes, url)
    await userEvent.type(await screen.findByLabelText('Search tables & columns…'), 'zzzz')
    expect(await screen.findByText('No tables or columns match.')).toBeInTheDocument()
    await userEvent.clear(screen.getByLabelText('Search tables & columns…'))
    await userEvent.click(screen.getByRole('button', { name: 'Views' }))
    expect(screen.getByRole('button', { name: 'Views' })).toHaveAttribute('aria-pressed', 'true')
    await waitFor(() => expect(screen.getAllByRole('treeitem').map((i) => i.textContent)).toEqual(['shop(1)']))
  })

  it('the saved source card links to the explorer', async () => {
    mockProjectConnections(pgConnection())
    server.use(http.get('*/api/v1/connectors', () => HttpResponse.json({ sources: [], targets: [] })))
    renderRoutes(routes, '/projects/p1/source')
    const link = await screen.findByRole('link', { name: 'Explore schema' })
    expect(link).toHaveAttribute('href', '/projects/p1/source/schema')
  })

  it('AC5: adds mapping and navigates to tables step', async () => {
    mockProject(makeProject({ source: repo('postgres'), target: repo('couchbase') }))
    mockSchema(shopSnapshot())
    mockKeyspaces()
    server.use(http.get('*/api/v1/projects/:pid/mappings', () => HttpResponse.json({ items: [] })))
    const { router } = renderRoutes(routes, `${url}?schema=shop&table=customer`)
    await userEvent.click(await screen.findByRole('link', { name: 'Add to project mappings' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/projects/p1/tables'))
    expect(await screen.findByRole('checkbox', { name: 'Select shop.customer' })).toBeChecked()
    expect(screen.getByRole('textbox', { name: 'Collection for shop.customer' })).toHaveValue('customer')
  })

  it('AC5: existing mapping shows Open mapping', async () => {
    mockSchema(shopSnapshot())
    server.use(
      http.get('*/api/v1/projects/:pid/mappings', () =>
        HttpResponse.json({
          items: [{ id: 'm7', sourceSchema: 'shop', sourceTable: 'customer', target: { bucket: 'shop', scope: 'sales', collection: 'customer' }, status: 'ready', stats: stats(), version: 2, updatedAt: new Date().toISOString() }],
        }),
      ),
    )
    renderRoutes(routes, `${url}?schema=shop&table=customer`)
    expect(await screen.findByRole('link', { name: 'Open mapping' })).toHaveAttribute('href', '/projects/p1/mappings/m7')
    expect(screen.queryByRole('link', { name: 'Add to project mappings' })).not.toBeInTheDocument()
  })
})
