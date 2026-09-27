import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { cbConnection, cbResult, mockCatalog, mockProjectConnections, pgConnection } from '@/test/connections'
import { server, setupMswServer } from '@/test/msw'
import { makeProject, mockProject, repo } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'

setupMswServer()
beforeEach(() => {
  mockSession('owner')
  mockCatalog()
})

describe('Target step (Couchbase)', () => {
  it('AC1: capella forces the couchbases scheme', async () => {
    mockProject(makeProject({ source: repo('postgres') }))
    mockProjectConnections(pgConnection())
    renderRoutes(routes, '/projects/p1/target')
    await userEvent.click(await screen.findByRole('radio', { name: 'Capella' }))
    expect(screen.getByText(/Allowed IP addresses/)).toBeInTheDocument()
    expect(screen.queryByText('Verify TLS certificates')).not.toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('Connection string'), 'couchbase://cb.x.cloud.couchbase.com')
    await userEvent.type(screen.getByLabelText('Username'), 'migrator')
    await userEvent.type(document.getElementById('cb-password')!, 'pw')
    await userEvent.click(screen.getByRole('button', { name: 'Test connection' }))
    expect(await screen.findByText('Capella connection strings start with couchbases://')).toBeInTheDocument()
  })

  it('AC6: test unsaved values then save and show summary card', async () => {
    mockProject(makeProject({ source: repo('postgres') }))
    mockProjectConnections(pgConnection())
    let testBody: Record<string, unknown> | undefined
    let putBody: Record<string, unknown> | undefined
    server.use(
      http.post('*/api/v1/connections/test', async ({ request }) => {
        testBody = (await request.json()) as Record<string, unknown>
        return HttpResponse.json(cbResult)
      }),
      http.put('*/api/v1/projects/p1/connections/target', async ({ request }) => {
        putBody = (await request.json()) as Record<string, unknown>
        mockProjectConnections(pgConnection(), cbConnection())
        mockProject(makeProject({ status: 'configured', source: repo('postgres'), target: repo('couchbase') }))
        return HttpResponse.json(cbConnection({ lastTestStatus: 'untested' }), { status: 201 })
      }),
      http.post('*/api/v1/connections/:id/test', () => HttpResponse.json(cbResult)),
    )
    renderRoutes(routes, '/projects/p1/target')
    await userEvent.click(await screen.findByRole('radio', { name: 'Capella' }))
    await userEvent.type(screen.getByLabelText('Connection string'), 'couchbases://cb.8f2k3x.cloud.couchbase.com')
    await userEvent.type(screen.getByLabelText('Username'), 'migrator')
    await userEvent.type(document.getElementById('cb-password')!, 'cb-password')
    await userEvent.click(screen.getByRole('button', { name: 'Test connection' }))
    expect(await screen.findByText('Couchbase Server 8.0.3')).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'What you can do here' })).toHaveTextContent('Write documents')
    expect(testBody).toMatchObject({ role: 'target', type: 'couchbase', config: { deployment: 'capella', connectionString: 'couchbases://cb.8f2k3x.cloud.couchbase.com' }, secrets: { password: 'cb-password' } })
    expect((testBody!.config as Record<string, unknown>).tlsVerify).toBeUndefined()

    await userEvent.click(screen.getByRole('button', { name: 'Save to project' }))
    await waitFor(() => expect(putBody).toBeDefined())
    expect(await screen.findByText('Target saved to project')).toBeInTheDocument()
    const summary = await screen.findByRole('region', { name: 'cb.8f2k3x.cloud.couchbase.com' })
    expect(summary).toHaveTextContent('Capella')
    expect(summary).toHaveTextContent('On · verified')
    expect(await screen.findByRole('link', { name: /Next: select tables/ })).toHaveAttribute('href', '/projects/p1/tables')
  })

  it('AC6: re-test from summary card sends no password', async () => {
    mockProject(makeProject({ status: 'configured', source: repo('postgres'), target: repo('couchbase') }))
    mockProjectConnections(pgConnection(), cbConnection())
    let body: string | null = 'unset'
    server.use(
      http.post('*/api/v1/connections/:id/test', async ({ request, params }) => {
        expect(params.id).toBe('p1::target')
        body = await request.text()
        return HttpResponse.json(cbResult)
      }),
    )
    renderRoutes(routes, '/projects/p1/target')
    await userEvent.click(await screen.findByRole('button', { name: 'Test connection' }))
    expect(await screen.findByText('Couchbase Server 8.0.3')).toBeInTheDocument()
    expect(body).toBe('')
  })
})
