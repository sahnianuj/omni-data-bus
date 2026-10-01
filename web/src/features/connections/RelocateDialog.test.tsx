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
import { relocated } from './RelocateDialog'

setupMswServer()

const dirConnection = cbConnection({ type: 'directory', name: 'odb-out', config: { path: '/data/odb-out' }, secrets: { passwordSet: false, caCertSet: false, clientCertSet: false } })
const dirMapping = (id: string, table: string, folder: string) => ({
  id,
  projectId: 'p1',
  sourceSchema: 'shop',
  sourceTable: table,
  target: { bucket: '', scope: '', collection: '' },
  location: { kind: 'directory', folder },
  status: 'draft',
  version: 1,
  updatedAt: new Date().toISOString(),
})

beforeEach(() => {
  mockSession('owner')
  mockCatalog()
  mockProject(makeProject({ status: 'configured', source: repo('postgres'), target: repo('directory') }))
  mockProjectConnections(pgConnection(), dirConnection)
  server.use(
    http.get('*/api/v1/target-kinds', () => HttpResponse.json({ kinds: targetKinds({ directoryEnabled: true }) })),
    http.get('*/api/v1/projects/:id/mappings', () =>
      HttpResponse.json({ items: [dirMapping('m1', 'Order Details', 'order_details/'), dirMapping('m2', 'customer', 'customer/')] }),
    ),
    http.post('*/api/v1/connections/test', () => HttpResponse.json(cbResult)),
    http.post('*/api/v1/connections/:id/test', () => HttpResponse.json(cbResult)),
  )
})

describe('RelocateDialog', () => {
  it('OC-068: new locations mirror the server', () => {
    expect(relocated({ kind: 'directory', folder: 'archive/Customer.v2/' }, 'customer', 'couchbase', { bucket: ' shop ', scope: 'sales' })).toEqual({
      kind: 'couchbase', bucket: 'shop', scope: 'sales', collection: 'customer_v_2', createIfMissing: true,
    })
    expect(relocated({ kind: 'couchbase', bucket: 'b', scope: 's', collection: 'order%items' }, 'x', 'directory', {})).toEqual({
      kind: 'directory', folder: 'order_items', createIfMissing: true,
    })
  })

  it('OC-068 AC4: move and save', async () => {
    const bodies: Record<string, unknown>[] = []
    server.use(
      http.put('*/api/v1/projects/p1/connections/target', async ({ request }) => {
        const body = (await request.json()) as Record<string, unknown>
        bodies.push(body)
        if (!body.relocate) {
          return HttpResponse.json(
            { type: 'about:blank', status: 409, code: 'TARGET_KIND_IN_USE', title: 'Target kind in use', detail: 'The target can’t change…' },
            { status: 409 },
          )
        }
        return HttpResponse.json(cbConnection())
      }),
    )
    renderRoutes(routes, '/projects/p1/target')
    await userEvent.click(await screen.findByRole('button', { name: 'Edit' }))
    await userEvent.click(within(await screen.findByRole('radiogroup', { name: 'Target kind' })).getByRole('radio', { name: /Couchbase cluster/ }))
    await userEvent.type(screen.getByLabelText('Connection string'), 'couchbase://localhost')
    await userEvent.type(screen.getByLabelText('Username'), 'Administrator')
    await userEvent.type(document.getElementById('cb-password')!, 'password')
    await userEvent.click(screen.getByRole('button', { name: 'Test connection' }))
    await screen.findByText('Couchbase Server 8.0.3')
    await userEvent.click(screen.getByRole('button', { name: 'Save to project' }))

    const dialog = await screen.findByRole('dialog', { name: 'Move mappings to the new target?' })
    expect(within(dialog).queryByText('The target can’t change…')).not.toBeInTheDocument()
    const list = within(dialog).getByRole('list', { name: 'Mappings to move' })
    await waitFor(() => expect(within(list).getAllByRole('listitem')).toHaveLength(2))
    const move = within(dialog).getByRole('button', { name: 'Move and save' })
    expect(move).toBeDisabled()
    await userEvent.type(within(dialog).getByLabelText('Bucket'), 'shop')
    expect(list).toHaveTextContent('📁 order_details/ → shop._default.order_details')
    await userEvent.click(move)

    expect(await screen.findByText('Target saved to project')).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(bodies).toHaveLength(2)
    expect(bodies[1]).toMatchObject({ type: 'couchbase', relocate: { bucket: 'shop', scope: '_default' } })
  })
})
