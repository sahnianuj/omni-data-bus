import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { cbConnection, cbResult, mockCatalog, mockProjectConnections, pgConnection } from '@/test/connections'
import { server, setupMswServer, targetKinds } from '@/test/msw'
import { makeProject, mockProject, repo } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'
import { formatLocation } from './kinds'

setupMswServer()
beforeEach(() => {
  mockSession('owner')
  mockCatalog()
  mockProject(makeProject({ source: repo('postgres') }))
  mockProjectConnections(pgConnection())
})

describe('TargetKindPicker', () => {
  it('AC5: switches form and browser by kind', async () => {
    server.use(http.get('*/api/v1/target-kinds', () => HttpResponse.json({ kinds: targetKinds({ directoryEnabled: true }) })))
    let testBody: Record<string, unknown> | undefined
    server.use(
      http.post('*/api/v1/connections/test', async ({ request }) => {
        testBody = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ ok: true, latencyMs: 3, steps: [] })
      }),
    )
    renderRoutes(routes, '/projects/p1/target')
    const kinds = await screen.findByRole('radiogroup', { name: 'Target kind' })
    // Couchbase is the default: its form, with credentials.
    expect(within(kinds).getByRole('radio', { name: /Couchbase cluster/ })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByLabelText('Connection string')).toBeInTheDocument()

    await userEvent.click(within(kinds).getByRole('radio', { name: /Directory of JSON files/ }))
    expect(screen.queryByLabelText('Connection string')).not.toBeInTheDocument()
    expect(document.getElementById('cb-password')).toBeNull()
    await userEvent.type(screen.getByLabelText('Folder path'), 'relative')
    await userEvent.click(screen.getByRole('button', { name: 'Test connection' }))
    expect(await screen.findByText('Enter an absolute path, such as /data/odb-out')).toBeInTheDocument()
    await userEvent.clear(screen.getByLabelText('Folder path'))
    await userEvent.type(screen.getByLabelText('Folder path'), '/data/odb-out')
    await userEvent.click(screen.getByRole('button', { name: 'Test connection' }))
    await waitFor(() => expect(testBody).toBeDefined())
    expect(testBody).toEqual({
      role: 'target', type: 'directory', secrets: {},
      config: { path: '/data/odb-out', createIfMissing: false, layout: 'flat', pretty: false, compression: 'none', fileMode: '0640', durability: 'none', manifest: true, maxDocBytes: 20971520 },
    })
    // The Couchbase-only capabilities card isn't shown for a directory.
    expect(screen.queryByRole('region', { name: 'What you can do here' })).not.toBeInTheDocument()
  })

  it('AC5: a disabled kind shows why and can’t be picked', async () => {
    server.use(http.get('*/api/v1/target-kinds', () => HttpResponse.json({ kinds: targetKinds({ directoryEnabled: false }) })))
    renderRoutes(routes, '/projects/p1/target')
    const dir = await screen.findByRole('radio', { name: /Directory of JSON files/ })
    expect(dir).toBeDisabled()
    expect(dir).toHaveAccessibleDescription(/no folder it can write to/)
  })

  it('formats locations by kind', () => {
    expect(formatLocation({ kind: 'couchbase', bucket: 'shop', scope: 'sales', collection: 'orders' })).toBe('shop.sales.orders')
    expect(formatLocation({ kind: 'directory', folder: 'orders' })).toBe('📁 orders/')
    expect(formatLocation({ kind: 'directory', folder: '/archive/2026/' })).toBe('📁 archive/2026/')
    expect(formatLocation(undefined)).toBe('')
  })

  it('OC-072 AC6: the all-in-one image offers its bundled Couchbase', async () => {
    const bundled = { kind: 'couchbase', label: 'Bundled Couchbase (this container)', buckets: ['sandbox'] }
    server.use(http.get('*/api/v1/target-kinds', () => HttpResponse.json({ kinds: targetKinds(), bundled })))
    let posted = 0
    server.use(
      http.post('*/api/v1/projects/p1/connections/target/bundled', () => {
        posted++
        mockProjectConnections(pgConnection(), cbConnection({ name: 'Bundled Couchbase', config: { deployment: 'self-managed', connectionString: 'couchbase://127.0.0.1', username: 'Administrator', kvTimeoutMs: 2500, connectTimeoutMs: 10000 } }))
        return HttpResponse.json({ connection: cbConnection({ name: 'Bundled Couchbase' }), test: cbResult }, { status: 201 })
      }),
    )
    renderRoutes(routes, '/projects/p1/target')
    const use = await screen.findByRole('button', { name: 'Use the bundled Couchbase' })
    expect(use).toHaveAttribute('title', expect.stringContaining('sandbox'))
    // Picking the directory kind hides it: it's a Couchbase target.
    await userEvent.click(screen.getByRole('radio', { name: /Directory of JSON files/ }))
    expect(screen.queryByRole('button', { name: 'Use the bundled Couchbase' })).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('radio', { name: /Couchbase cluster/ }))

    await userEvent.click(screen.getByRole('button', { name: 'Use the bundled Couchbase' }))
    await waitFor(() => expect(posted).toBe(1))
    // Saved and tested: the summary shows instead of the form, with the test result.
    expect(await screen.findByText('couchbase://127.0.0.1')).toBeInTheDocument()
    expect(screen.queryByLabelText('Connection string')).not.toBeInTheDocument()
  })

  it('OC-072 AC6: the regular image has no bundled button', async () => {
    renderRoutes(routes, '/projects/p1/target')
    await screen.findByRole('radiogroup', { name: 'Target kind' })
    expect(screen.queryByRole('button', { name: 'Use the bundled Couchbase' })).not.toBeInTheDocument()
  })
})
