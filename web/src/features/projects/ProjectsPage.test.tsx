import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { server, setupMswServer } from '@/test/msw'
import { makeProject, repo } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'

setupMswServer()
beforeEach(() => mockSession('owner'))

describe('ProjectsPage', () => {
  it('AC8: cards show source/target chips with status', async () => {
    server.use(
      http.get('*/api/v1/projects', () =>
        HttpResponse.json({
          items: [
            makeProject({ id: 'a', name: 'Customer 360', status: 'configured', source: repo('postgres'), target: repo('couchbase') }),
            makeProject({ id: 'b', name: 'Inventory sync' }),
          ],
          nextCursor: '',
        }),
      ),
    )
    renderRoutes(routes, '/projects')
    expect(await screen.findByText('Customer 360')).toBeInTheDocument()
    expect(screen.getByText('Source · PostgreSQL')).toBeInTheDocument()
    expect(screen.getByText('Target · Couchbase')).toBeInTheDocument()
    expect(screen.getAllByText('Connected')).toHaveLength(2)
    expect(screen.getByText('Source and target not set up yet')).toBeInTheDocument()
    expect(screen.getByText('Configured')).toBeInTheDocument()
  })

  it('shows the empty state', async () => {
    server.use(http.get('*/api/v1/projects', () => HttpResponse.json({ items: [], nextCursor: '' })))
    renderRoutes(routes, '/projects')
    expect(await screen.findByText('Create your first migration project')).toBeInTheDocument()
  })
})

describe('NewProjectDialog', () => {
  it('AC1: creates and routes to source step', async () => {
    let body: unknown
    server.use(
      http.get('*/api/v1/projects', () => HttpResponse.json({ items: [], nextCursor: '' })),
      http.post('*/api/v1/projects', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json(makeProject({ id: 'new1', name: 'Orders archive' }), { status: 201 })
      }),
      http.get('*/api/v1/projects/new1', () => HttpResponse.json(makeProject({ id: 'new1', name: 'Orders archive' }))),
    )
    const { router } = renderRoutes(routes, '/projects')
    await userEvent.click((await screen.findAllByRole('button', { name: 'New project' }))[0]!)
    await userEvent.type(screen.getByLabelText('Name'), 'Orders archive')
    await userEvent.click(screen.getByRole('button', { name: 'Create project' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/projects/new1/source'))
    expect(body).toEqual({ name: 'Orders archive', description: '' })
  })

  it('AC1: duplicate name shows the server field error', async () => {
    server.use(
      http.get('*/api/v1/projects', () => HttpResponse.json({ items: [], nextCursor: '' })),
      http.post('*/api/v1/projects', () =>
        HttpResponse.json(
          { title: 'Name already used', status: 409, code: 'PROJECT_NAME_TAKEN', fieldErrors: [{ field: 'name', message: 'is already used by another project' }] },
          { status: 409 },
        ),
      ),
    )
    renderRoutes(routes, '/projects?new=1')
    await userEvent.type(await screen.findByLabelText('Name'), 'Orders')
    await userEvent.click(screen.getByRole('button', { name: 'Create project' }))
    expect(await screen.findByText('Is already used by another project')).toBeInTheDocument()
  })
})
