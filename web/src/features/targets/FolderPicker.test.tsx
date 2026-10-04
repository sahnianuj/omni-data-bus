import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { mockCatalog, mockProjectConnections, pgConnection } from '@/test/connections'
import { server, setupMswServer, targetKinds } from '@/test/msw'
import { makeProject, mockProject, repo } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'
import { hostPathOf } from './kinds'

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

// OC-080: the all-in-one image shares ~/omni-data-bus-output as /data.
describe('FolderPicker in the all-in-one image', () => {
  const shared = { path: '/data', hostPath: '/Users/ana/omni-data-bus-output' }
  const sharedKinds = () => targetKinds({ directoryEnabled: true }).map((k) => (k.type === 'directory' ? { ...k, sharedFolder: shared } : k))

  it('OC-080 AC2: the shared folder is labeled with its place on the computer', async () => {
    server.use(
      http.get('*/api/v1/target-kinds', () => HttpResponse.json({ kinds: sharedKinds() })),
      http.get('*/api/v1/target-kinds/directory/folders', ({ request }) => {
        const path = new URL(request.url).searchParams.get('path') ?? ''
        if (!path) return HttpResponse.json({ path: '', parent: '', canCreate: false, folders: [{ name: '/data', path: '/data', where: 'computer', hostPath: shared.hostPath, label: `Your computer: ${shared.hostPath}` }] })
        return HttpResponse.json({ path, parent: '', canCreate: true, where: 'computer', hostPath: hostPathOf(path, shared), folders: [] })
      }),
    )
    const user = userEvent.setup()
    renderRoutes(routes, '/projects/p1/target')
    await user.click(await screen.findByRole('radio', { name: /Directory of JSON files/ }))
    // The field's hint says where to choose, before anything is picked.
    expect(screen.getByText("Choose a folder in /data: that's /Users/ana/omni-data-bus-output on your computer.")).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Choose folder…' }))
    const dialog = await screen.findByRole('dialog', { name: 'Choose the output folder' })
    expect(dialog).toHaveTextContent('Files go to /Users/ana/omni-data-bus-output on your computer.')
    expect(within(dialog).queryByRole('note')).not.toBeInTheDocument()
    await user.click(await within(dialog).findByRole('button', { name: /Your computer: \/Users\/ana\/omni-data-bus-output/ }))
    expect(await within(dialog).findByText(/on your computer: \/Users\/ana\/omni-data-bus-output$/)).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Use this folder' }))
    expect(screen.getByLabelText('Folder path')).toHaveValue('/data')
    expect(screen.getByText('on your computer: /Users/ana/omni-data-bus-output')).toBeInTheDocument()
  })

  it('OC-080 AC2: nothing shared: warns that files stay in the container', async () => {
    server.use(
      http.get('*/api/v1/target-kinds/directory/folders', () =>
        HttpResponse.json({ path: '', parent: '', canCreate: false, folders: [{ name: '/opt/couchbase/var/odb/home', path: '/opt/couchbase/var/odb/home', where: 'container', label: "Inside the app's container (not visible on your computer)" }] }),
      ),
    )
    const user = userEvent.setup()
    renderRoutes(routes, '/projects/p1/target')
    await user.click(await screen.findByRole('radio', { name: /Directory of JSON files/ }))
    await user.click(screen.getByRole('button', { name: 'Choose folder…' }))
    const dialog = await screen.findByRole('dialog', { name: 'Choose the output folder' })
    expect(await within(dialog).findByRole('note')).toHaveTextContent('./run.sh start --output <folder>')
    expect(within(dialog).getByRole('button', { name: /Inside the app's container/ })).toBeInTheDocument()
  })
})

describe('hostPathOf', () => {
  it('OC-080: maps a container path to the computer', () => {
    const s = { path: '/data', hostPath: '/Users/ana/out' }
    expect(hostPathOf('/data/orders/2026', s)).toBe('/Users/ana/out/orders/2026')
    expect(hostPathOf('/data', s)).toBe('/Users/ana/out')
    expect(hostPathOf('/database', s)).toBe('')
    expect(hostPathOf('/data/x', undefined)).toBe('')
    expect(hostPathOf('/data/orders', { path: '/data', hostPath: 'C:\\Users\\ana\\out' })).toBe('C:\\Users\\ana\\out\\orders')
  })
})
