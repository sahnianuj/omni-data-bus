import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { mockCatalog, mockProjectConnections } from '@/test/connections'
import { server, setupMswServer } from '@/test/msw'
import { makeProject, mockProject, repo } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'

setupMswServer()
beforeEach(() => {
  mockSession('owner')
  mockCatalog()
  mockProjectConnections(null)
})

describe('ProjectWizard', () => {
  it('names the target repository by its kind', async () => {
    const step = () => screen.getByRole('navigation', { name: 'Project steps' })
    mockProject(makeProject({ source: repo('postgres'), target: repo('directory') }))
    const dir = renderRoutes(routes, '/projects/p1/source')
    await waitFor(() => expect(step()).toHaveTextContent('Target repositoryDirectory of JSON files'))
    dir.unmount()
    mockProject(makeProject({ source: repo('postgres'), target: repo('couchbase') }))
    const cb = renderRoutes(routes, '/projects/p1/source')
    await waitFor(() => expect(step()).toHaveTextContent('Target repositoryCouchbase'))
    cb.unmount()
    mockProject(makeProject({ source: repo('postgres') }))
    renderRoutes(routes, '/projects/p1/source')
    await waitFor(() => expect(step()).toHaveTextContent('Target repositoryCouchbase or JSON files'))
  })

  it('AC5: resumes at first incomplete step', async () => {
    mockProject(makeProject({ source: repo('postgres') }))
    const { router } = renderRoutes(routes, '/projects/p1')
    await waitFor(() => expect(router.state.location.pathname).toBe('/projects/p1/target'))
  })

  it('AC2: later steps locked until both repositories tested OK', async () => {
    mockProject(makeProject())
    renderRoutes(routes, '/projects/p1/source')
    const rail = await screen.findByRole('navigation', { name: 'Project steps' })
    expect(rail).toHaveTextContent('Set up and test the source and target to unlock the next steps.')
    expect(screen.queryByRole('link', { name: /Tables/ })).not.toBeInTheDocument()
    expect(screen.getByText('Tables').closest('[aria-disabled]')).toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByRole('link', { name: /Source repository/ })).toHaveAttribute('aria-current', 'step')
    expect(screen.getByText('Source · not saved')).toBeInTheDocument()
  })

  it('AC2: tables unlock once both repositories pass', async () => {
    mockProject(makeProject({ status: 'configured', source: repo('postgres'), target: repo('couchbase') }))
    renderRoutes(routes, '/projects/p1/target')
    expect(await screen.findByRole('link', { name: /Tables/ })).toBeInTheDocument()
    expect(screen.getByText('shop@db-prod-1')).toBeInTheDocument()
  })

  it('AC6: renaming autosaves with the version', async () => {
    mockProject(makeProject())
    let body: unknown
    server.use(
      http.patch('*/api/v1/projects/p1', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json(makeProject({ name: 'Inventory sync v2', version: 2 }))
      }),
    )
    renderRoutes(routes, '/projects/p1/source')
    const input = await screen.findByRole('textbox', { name: 'Project name' })
    await userEvent.type(input, ' v2')
    await waitFor(() => expect(body).toEqual({ name: 'Inventory sync v2', version: 1 }), { timeout: 3000 })
    expect(await screen.findByText('Saved · just now')).toBeInTheDocument()
  })

  it('AC7: delete requires the typed name and returns to the list', async () => {
    mockProject(makeProject())
    let deleted = false
    server.use(
      http.delete('*/api/v1/projects/p1', () => {
        deleted = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { router } = renderRoutes(routes, '/projects/p1/source')
    await userEvent.click(await screen.findByRole('button', { name: 'Delete project' }))
    const confirm = screen.getAllByRole('button', { name: 'Delete project' }).at(-1)!
    expect(confirm).toBeDisabled()
    await userEvent.type(screen.getByRole('textbox', { name: /Type/ }), 'Inventory sync')
    await userEvent.click(confirm)
    await waitFor(() => expect(router.state.location.pathname).toBe('/projects'))
    expect(deleted).toBe(true)
  })

  it('viewers cannot rename or delete', async () => {
    mockSession('viewer')
    mockProject(makeProject())
    renderRoutes(routes, '/projects/p1/source')
    const input = await screen.findByRole('textbox', { name: 'Project name' })
    await waitFor(() => expect(input).toHaveAttribute('readonly'))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Delete project' })).toBeDisabled())
  })

  it('AC2: source and target steps render form with test button', async () => {
    mockProject(makeProject())
    const { router } = renderRoutes(routes, '/projects/p1/source')
    expect(await screen.findByRole('radio', { name: /PostgreSQL/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Test connection' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save to project' })).toBeInTheDocument()
    await router.navigate('/projects/p1/target')
    expect(await screen.findByLabelText('Connection string')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Test connection' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save to project' })).toBeInTheDocument()
  })
})
