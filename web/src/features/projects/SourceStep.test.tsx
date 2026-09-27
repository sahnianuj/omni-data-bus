import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { mockCatalog, mockProjectConnections, okResult, pgConnection } from '@/test/connections'
import { server, setupMswServer } from '@/test/msw'
import { makeProject, mockProject, repo } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'

setupMswServer()
beforeEach(() => {
  mockSession('owner')
  mockCatalog()
})

async function fillForm() {
  await userEvent.type(await screen.findByLabelText('Host'), 'db.example.com')
  await userEvent.type(screen.getByLabelText('Database'), 'shop')
  await userEvent.type(screen.getByLabelText('Username'), 'omni_ro')
  await userEvent.type(document.getElementById('pg-password')!, 'typed-password')
}

describe('Source step', () => {
  it('AC1: zod errors shown inline without calling the server', async () => {
    mockProject(makeProject())
    mockProjectConnections(null)
    let called = false
    server.use(http.post('*/api/v1/connections/test', () => ((called = true), HttpResponse.json(okResult))))
    renderRoutes(routes, '/projects/p1/source')
    await userEvent.clear(await screen.findByLabelText('Port'))
    await userEvent.type(screen.getByLabelText('Port'), '70000')
    await userEvent.click(screen.getByRole('button', { name: 'Test connection' }))
    expect(await screen.findByText('Enter a host name or IP address')).toBeInTheDocument()
    expect(screen.getByText('Port must be between 1 and 65535')).toBeInTheDocument()
    expect(screen.getByText('Enter the password')).toBeInTheDocument()
    expect(called).toBe(false)
  })

  it('AC2: pasting a connection string fills the form (password included, URI not kept)', async () => {
    mockProject(makeProject())
    mockProjectConnections(null)
    renderRoutes(routes, '/projects/p1/source')
    await userEvent.click(await screen.findByRole('button', { name: 'Paste connection string' }))
    await userEvent.type(screen.getByLabelText('Paste connection string'), 'postgresql://app:s3cr%40t@db.example.com:6543/shop?sslmode=verify-full')
    await userEvent.click(screen.getByRole('button', { name: 'Apply' }))
    expect(screen.getByLabelText('Host')).toHaveValue('db.example.com')
    expect(screen.getByLabelText('Port')).toHaveValue('6543')
    expect(document.getElementById('pg-password')).toHaveValue('s3cr@t')
    expect(screen.getByRole('radio', { name: 'verify-full' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.queryByLabelText('Paste connection string')).not.toBeInTheDocument()
  })

  it('AC6: test button posts unsaved config and persists nothing', async () => {
    mockProject(makeProject())
    mockProjectConnections(null)
    let body: Record<string, unknown> | undefined
    let saved = false
    server.use(
      http.post('*/api/v1/connections/test', async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>
        return HttpResponse.json(okResult)
      }),
      http.put('*/api/v1/projects/:id/connections/:role', () => ((saved = true), HttpResponse.json({}))),
    )
    renderRoutes(routes, '/projects/p1/source')
    await fillForm()
    await userEvent.click(screen.getByRole('button', { name: 'Test connection' }))
    expect(await screen.findByText('Passed with 1 warning')).toBeInTheDocument()
    expect(body).toMatchObject({ role: 'source', type: 'postgres', config: { host: 'db.example.com', port: 5432, database: 'shop', username: 'omni_ro', sslMode: 'require' }, secrets: { password: 'typed-password' } })
    expect(saved).toBe(false)
    expect(screen.getByText(/nothing saved yet/)).toBeInTheDocument()
  })

  it('AC6: confirms when untested, then collapses to the summary card', async () => {
    mockProject(makeProject())
    mockProjectConnections(null)
    let putBody: Record<string, unknown> | undefined
    server.use(
      http.put('*/api/v1/projects/p1/connections/source', async ({ request }) => {
        putBody = (await request.json()) as Record<string, unknown>
        mockProjectConnections(pgConnection())
        mockProject(makeProject({ source: repo('postgres', 'warn') }))
        return HttpResponse.json(pgConnection({ lastTestStatus: 'untested' }), { status: 201 })
      }),
      http.post('*/api/v1/connections/:id/test', () => HttpResponse.json(okResult)),
    )
    renderRoutes(routes, '/projects/p1/source')
    await fillForm()
    await userEvent.click(screen.getByRole('button', { name: 'Save to project' }))
    expect(await screen.findByText('Save without a successful test?')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Save anyway' }))
    expect(await screen.findByText('Source saved to project')).toBeInTheDocument()
    expect(putBody).toMatchObject({ type: 'postgres', secrets: { password: 'typed-password' } })
    const summary = await screen.findByRole('region', { name: 'shop@db.example.com' })
    expect(summary).toHaveTextContent('db.example.com:5432/shop')
    expect(summary).toHaveTextContent('Stored, encrypted')
    expect(await screen.findByRole('link', { name: /Next: target repository/ })).toHaveAttribute('href', '/projects/p1/target')
  })

  it('AC5: summary card after save with re-test using stored credentials', async () => {
    mockProject(makeProject({ source: repo('postgres', 'warn') }))
    mockProjectConnections(pgConnection())
    let testBody: string | null = 'unset'
    server.use(
      http.post('*/api/v1/connections/:id/test', async ({ request }) => {
        testBody = await request.text()
        return HttpResponse.json(okResult)
      }),
    )
    renderRoutes(routes, '/projects/p1/source')
    const summary = await screen.findByRole('region', { name: 'shop@db.example.com' })
    expect(summary).toHaveTextContent('Connected with warnings')
    expect(summary).toHaveTextContent('omni_ro')
    expect(screen.queryByLabelText('Password')).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Test connection' }))
    expect(await screen.findByText('Passed with 1 warning')).toBeInTheDocument()
    expect(testBody).toBe('')
  })

  it('AC5: edit shows the stored password placeholder, not the password', async () => {
    mockProject(makeProject({ source: repo('postgres') }))
    mockProjectConnections(pgConnection())
    let putBody: Record<string, unknown> | undefined
    server.use(
      http.post('*/api/v1/connections/test', () => HttpResponse.json(okResult)),
      http.put('*/api/v1/projects/p1/connections/source', async ({ request }) => {
        putBody = (await request.json()) as Record<string, unknown>
        return HttpResponse.json(pgConnection())
      }),
      http.post('*/api/v1/connections/:id/test', () => HttpResponse.json(okResult)),
    )
    renderRoutes(routes, '/projects/p1/source')
    await userEvent.click(await screen.findByRole('button', { name: 'Edit' }))
    expect(document.getElementById('pg-password')).toHaveValue('•••••••• (stored)')
    expect(screen.getByLabelText('Host')).toHaveValue('db.example.com')
    await userEvent.click(screen.getByRole('button', { name: 'Test connection' }))
    await screen.findByText('Passed with 1 warning')
    await userEvent.click(screen.getByRole('button', { name: 'Save to project' }))
    await waitFor(() => expect(putBody).toBeDefined())
    expect(putBody!.secrets).toEqual({}) // the stored password is kept, not re-sent
  })

  it('viewers see a read-only message when nothing is set up', async () => {
    mockSession('viewer')
    mockProject(makeProject())
    mockProjectConnections(null)
    renderRoutes(routes, '/projects/p1/source')
    expect(await screen.findByText('Ask an editor or owner to set up this repository.')).toBeInTheDocument()
  })
})
