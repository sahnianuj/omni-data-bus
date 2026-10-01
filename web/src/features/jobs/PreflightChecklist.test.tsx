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

    await userEvent.click(screen.getByRole('button', { name: 'Run checks' }))
    const list = await screen.findByRole('region', { name: 'Pre-flight checks' })
    expect(within(list).getByText('1 check failed')).toBeInTheDocument()
    expect(within(list).getByText('Connection refused')).toBeInTheDocument()
    expect(within(list).getByText('Check the host and port.')).toBeInTheDocument()
    expect(within(list).getByText('shop.sales.customer already holds about 12 documents.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Migrate' })).toBeDisabled()
    expect(state.preflights[0]).toMatchObject({ writeMode: 'upsert', batchSize: 1000 })
    expect(state.creates).toHaveLength(0)
  })

  it('Migrate runs the checks, confirms with a summary and opens the job', async () => {
    const state = mockJobsApi(passing)
    const { router } = renderRoutes(routes, '/projects/p1/review')
    await userEvent.click(await screen.findByRole('button', { name: 'Migrate' }))
    const dialog = await screen.findByRole('dialog', { name: 'Start the migration?' })
    expect(dialog).toHaveTextContent('≈ 42k')
    expect(dialog).toHaveTextContent('shop.customer → shop.sales.customer')
    expect(within(dialog).getByRole('list', { name: 'Warnings' })).toHaveTextContent('already holds about 12 documents')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Start migration' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/projects/p1/jobs/j1'))
    expect(state.creates).toHaveLength(1)
  })

  it('an active job links to it instead of starting another', async () => {
    mockProject(makeProject({ source: repo('postgres'), target: repo('couchbase'), mappings: 1, readyMappings: 1, latestJob: { id: 'j9', status: 'running' } }))
    mockJobsApi(passing)
    renderRoutes(routes, '/projects/p1/review')
    expect(await screen.findByRole('link', { name: 'Open migration' })).toHaveAttribute('href', '/projects/p1/jobs/j9')
    expect(screen.getByRole('button', { name: 'Migrate' })).toBeDisabled()
  })
})
