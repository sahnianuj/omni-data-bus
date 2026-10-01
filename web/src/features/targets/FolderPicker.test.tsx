import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { mockCatalog, mockProjectConnections, pgConnection } from '@/test/connections'
import { server, setupMswServer } from '@/test/msw'
import { makeProject, mockProject, repo } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'

setupMswServer()

// A server with a home folder and /data.
const tree: Record<string, string[]> = {
  '': ['/Users/ada', '/data'],
  '/Users/ada': ['Documents', 'exports'],
  '/Users/ada/exports': [],
}
const listing = (path: string) => ({
  path,
  parent: path === '' || path === '/Users/ada' || path === '/data' ? '' : path.slice(0, path.lastIndexOf('/')),
  canCreate: path !== '',
  folders: (tree[path] ?? []).map((f) => (path ? { name: f, path: `${path}/${f}` } : { name: f, path: f })),
})

beforeEach(() => {
  mockSession('owner')
  mockCatalog()
  mockProject(makeProject({ source: repo('postgres') }))
  mockProjectConnections(pgConnection())
  server.use(
    http.get('*/api/v1/target-kinds/directory/folders', ({ request }) => HttpResponse.json(listing(new URL(request.url).searchParams.get('path') ?? ''))),
    http.post('*/api/v1/target-kinds/directory/folders', async ({ request }) => {
      const b = (await request.json()) as { parent: string; name: string }
      tree[b.parent] = [...(tree[b.parent] ?? []), b.name]
      tree[`${b.parent}/${b.name}`] = []
      return HttpResponse.json({ path: `${b.parent}/${b.name}` }, { status: 201 })
    }),
  )
})

describe('FolderPicker', () => {
  it('OC-038: pick the output folder by browsing, creating one on the way', async () => {
    const user = userEvent.setup()
    renderRoutes(routes, '/projects/p1/target')
    await user.click(await screen.findByRole('radio', { name: /Directory of JSON files/ }))
    await user.click(screen.getByRole('button', { name: 'Choose folder…' }))

    const dialog = await screen.findByRole('dialog', { name: 'Choose the output folder' })
    // The areas first: nothing to use yet.
    expect(within(dialog).getByRole('button', { name: 'Use this folder' })).toBeDisabled()
    await user.click(await within(dialog).findByRole('button', { name: /\/Users\/ada/ }))
    await user.click(await within(dialog).findByRole('button', { name: /exports/ }))
    expect(await within(dialog).findByText(/No folders here/)).toBeInTheDocument()

    // Create a sub-folder: the picker opens it.
    await user.click(within(dialog).getByRole('button', { name: 'New folder' }))
    await user.type(within(dialog).getByLabelText('Folder name'), 'Shop Output')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(within(dialog).getByText('/Users/ada/exports/Shop Output')).toBeInTheDocument())

    // Up goes back one folder; use the new folder.
    await user.click(within(dialog).getByRole('button', { name: 'Up one folder' }))
    await within(dialog).findByRole('button', { name: /Shop Output/ })
    await user.click(within(dialog).getByRole('button', { name: /Shop Output/ }))
    await user.click(within(dialog).getByRole('button', { name: 'Use this folder' }))

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.getByLabelText('Folder path')).toHaveValue('/Users/ada/exports/Shop Output')
    expect(screen.getByText('Document 1001 of orders → /Users/ada/exports/Shop Output/orders/1001.json')).toBeInTheDocument()
  })

  it('rejects invalid folder names before asking the server', async () => {
    const user = userEvent.setup()
    renderRoutes(routes, '/projects/p1/target')
    await user.click(await screen.findByRole('radio', { name: /Directory of JSON files/ }))
    await user.click(screen.getByRole('button', { name: 'Choose folder…' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(await within(dialog).findByRole('button', { name: /\/data/ }))
    await user.click(await within(dialog).findByRole('button', { name: 'New folder' }))
    await user.type(within(dialog).getByLabelText('Folder name'), '.secret')
    expect(within(dialog).getByRole('button', { name: 'Create' })).toBeDisabled()
  })
})
