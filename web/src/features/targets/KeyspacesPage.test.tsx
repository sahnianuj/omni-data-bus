import { screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { cbConnection, mockProjectConnections } from '@/test/connections'
import { mockKeyspaces } from '@/test/keyspaces'
import { server, setupMswServer } from '@/test/msw'
import { makeProject, mockProject, repo } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'

setupMswServer()
beforeEach(() => mockSession('owner'))

describe('KeyspacesPage', () => {
  it('browses the project target', async () => {
    mockProject(makeProject({ source: repo('postgres'), target: repo('couchbase') }))
    mockKeyspaces()
    renderRoutes(routes, '/projects/p1/target/keyspaces')
    expect(await screen.findByRole('heading', { name: 'Target keyspaces' })).toBeInTheDocument()
    expect(await screen.findByRole('combobox', { name: 'Bucket' })).toBeInTheDocument()
  })

  it('without a target it points back to the target step', async () => {
    mockProject(makeProject())
    renderRoutes(routes, '/projects/p1/target/keyspaces')
    expect(await screen.findByText('No target repository yet')).toBeInTheDocument()
  })

  it('the saved target card links to the keyspaces', async () => {
    mockProject(makeProject({ source: repo('postgres'), target: repo('couchbase') }))
    mockProjectConnections(null, cbConnection({ lastTestStatus: 'ok' }))
    server.use(http.get('*/api/v1/connectors', () => HttpResponse.json({ sources: [], targets: [] })))
    renderRoutes(routes, '/projects/p1/target')
    expect(await screen.findByRole('link', { name: 'Browse keyspaces' })).toHaveAttribute('href', '/projects/p1/target/keyspaces')
  })
})
