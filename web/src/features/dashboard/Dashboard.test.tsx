import { screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { server, setupMswServer } from '@/test/msw'
import { makeProject } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'

setupMswServer()
beforeEach(() => mockSession('owner'))

describe('DashboardPage', () => {
  it('AC8: renders tiles', async () => {
    server.use(
      http.get('*/api/v1/dashboard', () =>
        HttpResponse.json({
          stats: { projects: 3, runningJobs: 0, docsMigrated30d: 1234567, repositoriesHealthy: 3, repositoriesTotal: 4, configuredProjects: 1 },
          recent: [makeProject({ name: 'Customer 360' })],
        }),
      ),
    )
    renderRoutes(routes, '/')
    expect(await screen.findByText('1.2M')).toBeInTheDocument()
    expect(screen.getByText('3 / 4')).toBeInTheDocument()
    expect(screen.getByText('Customer 360')).toBeInTheDocument()
    expect(screen.getByText('Continue where you left off')).toBeInTheDocument()
  })

  it('shows a welcome state with no projects', async () => {
    server.use(
      http.get('*/api/v1/dashboard', () =>
        HttpResponse.json({ stats: { projects: 0, runningJobs: 0, docsMigrated30d: 0, repositoriesHealthy: 0, repositoriesTotal: 0, configuredProjects: 0 }, recent: [] }),
      ),
    )
    renderRoutes(routes, '/')
    expect(await screen.findByText('Welcome to Omni-Data-Bus')).toBeInTheDocument()
  })
})
