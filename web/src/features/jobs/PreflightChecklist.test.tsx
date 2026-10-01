import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { MappingSummary } from '@/features/mapping/types'
import { stats } from '@/test/mapping'
import { server, setupMswServer } from '@/test/msw'
import { makeProject, mockProject, repo } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'
import type { JobOptions, PreflightResult } from './api'

setupMswServer()

const mapping = (table: string, status: MappingSummary['status']): MappingSummary => ({
  id: `m-${table}`, sourceSchema: 'shop', sourceTable: table, mode: 'canvas', driving: { schema: 'shop', name: table }, target: { bucket: 'shop', scope: 'sales', collection: table },
  location: { kind: 'couchbase', bucket: 'shop', scope: 'sales', collection: table },
  status, stats: stats(), version: 1, updatedAt: new Date().toISOString(),
})

const passing: PreflightResult = {
  ok: true, totalRows: 42000,
  mappings: [{ mappingId: 'm-customer', table: 'shop.customer', target: { bucket: 'shop', scope: 'sales', collection: 'customer' }, location: { kind: 'couchbase', bucket: 'shop', scope: 'sales', collection: 'customer' }, rowEstimate: 42000 }],
  checks: [
    { id: 'source', label: 'Source repository reachable', status: 'ok' },
    { id: 'target', label: 'Target repository reachable (KV service)', status: 'ok' },
    { id: 'mapping.shop.customer.empty', label: 'shop.customer: target collection is empty', status: 'warn', code: 'COLLECTION_NOT_EMPTY', detail: 'shop.sales.customer already holds about 12 documents.' },
  ],
}

const failing: PreflightResult = {
  ...passing, ok: false,
  checks: [
    { id: 'source', label: 'Source repository reachable', status: 'fail', detail: 'Connection refused', hint: 'Check the host and port.' },
    ...passing.checks.slice(1),
  ],
}

function mockJobsApi(pre: PreflightResult) {
  const state = { preflights: [] as JobOptions[], creates: [] as JobOptions[] }
  server.use(
    http.get('*/api/v1/projects/:pid/mappings', () => HttpResponse.json({ items: [mapping('customer', 'ready'), mapping('orders', 'draft')] })),
    http.post('*/api/v1/projects/:pid/jobs/preflight', async ({ request }) => {
      state.preflights.push((await request.json()) as JobOptions)
      return HttpResponse.json(pre)
    }),
    http.post('*/api/v1/projects/:pid/jobs', async ({ request }) => {
      state.creates.push((await request.json()) as JobOptions)
      return HttpResponse.json({ job: { id: 'j1', status: 'queued', progress: { percent: 0 } }, preflight: pre }, { status: 201 })
    }),
    http.get('*/api/v1/jobs/:jid', () => HttpResponse.json({ id: 'j1', status: 'queued', progress: { percent: 0 } })),
  )
  return state
}

beforeEach(() => {
  mockSession('owner')
  mockProject(makeProject({ source: repo('postgres'), target: repo('couchbase'), mappings: 2, readyMappings: 1 }))
})

describe('PreflightChecklist', () => {
  it('AC2: renders checks and blocks Migrate on failure', async () => {
    const state = mockJobsApi(failing)
    renderRoutes(routes, '/projects/p1/review')
    expect(await screen.findByText('1 of 2 mappings will be migrated.')).toBeInTheDocument()

    // OC-078: the checks run on their own when the step opens.
    const list = await screen.findByRole('region', { name: 'Pre-flight checks' })
    expect(within(list).getByText('1 check failed')).toBeInTheDocument()
    expect(within(list).getByText('Connection refused')).toBeInTheDocument()
    expect(within(list).getByText('Check the host and port.')).toBeInTheDocument()
    expect(within(list).getByText('shop.sales.customer already holds about 12 documents.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Migrate' })).toBeDisabled()
    expect(state.preflights[0]).toMatchObject({ writeMode: 'upsert', batchSize: 1000 })
    expect(state.creates).toHaveLength(0)
  })

  it('OC-078 AC1: checks run on their own and Migrate starts without a dialog', async () => {
    const state = mockJobsApi(passing)
    const { router } = renderRoutes(routes, '/projects/p1/review')
    // Warnings show on the page before Migrate, with the row estimate next to it.
    const list = await screen.findByRole('region', { name: 'Pre-flight checks' })
    expect(within(list).getByText('shop.sales.customer already holds about 12 documents.')).toBeInTheDocument()
    expect(await screen.findByText('≈ 42k rows · Upsert')).toBeInTheDocument()
    expect(state.preflights).toHaveLength(1)
    await userEvent.click(screen.getByRole('button', { name: 'Migrate' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/projects/p1/jobs/j1'))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(state.creates).toHaveLength(1)
    expect(state.preflights).toHaveLength(1)
  })

  it('OC-078 AC2: changing an option checks again before Migrate is enabled', async () => {
    const state = mockJobsApi(passing)
    renderRoutes(routes, '/projects/p1/review')
    await screen.findByText('≈ 42k rows · Upsert')
    await userEvent.click(screen.getByRole('button', { name: /Advanced options/ }))
    const batch = screen.getByRole('spinbutton', { name: /Batch size/ })
    await userEvent.clear(batch)
    await userEvent.type(batch, '500')
    await waitFor(() => expect(state.preflights.at(-1)).toMatchObject({ batchSize: 500 }))
    expect(state.preflights).toHaveLength(2) // one on open, one after typing settles
    await waitFor(() => expect(screen.getByRole('button', { name: 'Migrate' })).toBeEnabled())
  })

  it('OC-078: a viewer doesn’t run checks', async () => {
    mockSession('viewer')
    const state = mockJobsApi(passing)
    renderRoutes(routes, '/projects/p1/review')
    expect(await screen.findByText('1 of 2 mappings will be migrated.')).toBeInTheDocument()
    await new Promise((r) => setTimeout(r, 600))
    expect(state.preflights).toHaveLength(0)
  })

  it('an active job links to it instead of starting another', async () => {
    mockProject(makeProject({ source: repo('postgres'), target: repo('couchbase'), mappings: 1, readyMappings: 1, latestJob: { id: 'j9', status: 'running' } }))
    mockJobsApi(passing)
    renderRoutes(routes, '/projects/p1/review')
    expect(await screen.findByRole('link', { name: 'Open migration' })).toHaveAttribute('href', '/projects/p1/jobs/j9')
    expect(screen.getByRole('button', { name: 'Migrate' })).toBeDisabled()
  })
})
