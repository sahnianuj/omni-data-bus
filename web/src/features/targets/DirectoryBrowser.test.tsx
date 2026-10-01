import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { server, setupMswServer } from '@/test/msw'
import { makeProject, mockProject, repo } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'

setupMswServer()

const folder = (path: string, docCount: number, docCountMore = false) => ({
  name: path.split('/').at(-1)!, level: 'folder' as const, path, leaf: true, canCreate: true, docCount, docCountMore,
  location: { kind: 'directory' as const, folder: path },
})

beforeEach(() => {
  mockSession('owner')
  mockProject(makeProject({ source: repo('postgres'), target: { id: 'p1::target', type: 'directory', name: '/data/odb-out', lastTestStatus: 'ok' } }))
})

describe('DirectoryBrowser', () => {
  it('AC3: lists and creates folders', async () => {
    const tree: Record<string, ReturnType<typeof folder>[]> = {
      '': [folder('customers', 2), folder('orders', 10000, true)],
      orders: [folder('orders/2026', 3)],
    }
    const created: unknown[] = []
    server.use(
      http.get('*/api/v1/connections/:id/locations', ({ request }) => {
        const parent = new URL(request.url).searchParams.get('parent') ?? ''
        return HttpResponse.json({ kind: 'directory', parent, nodes: tree[parent] ?? [] })
      }),
      http.post('*/api/v1/connections/:id/locations', async ({ request }) => {
        const body = (await request.json()) as { folder: string }
        created.push(body)
        tree[''] = [...tree['']!, folder(body.folder, 0)]
        return HttpResponse.json({ status: 'ready', location: { kind: 'directory', folder: body.folder } }, { status: 201 })
      }),
    )
    const user = userEvent.setup()
    renderRoutes(routes, '/projects/p1/target/keyspaces')
    const list = await screen.findByRole('list', { name: 'Folders' })
    expect(within(list).getByText('customers/')).toBeInTheDocument()
    expect(within(list).getByText('2 files')).toBeInTheDocument()
    expect(within(list).getByText('10k+ files')).toBeInTheDocument()

    // Open a folder, then go back to the root through the path.
    await user.click(within(list).getByText('orders/'))
    expect(await screen.findByText('2026/')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '/data/odb-out' }))
    await screen.findByText('customers/')

    // Names are checked like the server; a valid one is created.
    const input = screen.getByLabelText('New folder')
    await user.type(input, '../etc')
    expect(screen.getByRole('button', { name: 'Create folder' })).toBeDisabled()
    await user.clear(input)
    await user.type(input, 'archive')
    await user.click(screen.getByRole('button', { name: 'Create folder' }))
    await waitFor(() => expect(created).toEqual([{ kind: 'directory', folder: 'archive' }]))
    expect(await screen.findByText('archive/')).toBeInTheDocument()
  })
})
