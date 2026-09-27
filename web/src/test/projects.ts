import { http, HttpResponse } from 'msw'
import type { Project } from '@/features/projects/api'
import { server } from './msw'

export const repo = (type: 'postgres' | 'couchbase', status: 'ok' | 'warn' | 'failed' | 'untested' = 'ok') => ({
  id: `p1::${type === 'postgres' ? 'source' : 'target'}`,
  type,
  name: type === 'postgres' ? 'shop@db-prod-1' : 'cb.8f2k3x.cloud',
  lastTestStatus: status,
})

export const makeProject = (over: Partial<Project> = {}): Project => ({
  id: 'p1',
  name: 'Inventory sync',
  description: 'Products and stock levels.',
  status: 'draft',
  version: 1,
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
  source: null,
  target: null,
  ...over,
})

/** Serves GET /projects/:id (and the list) from an in-memory project. */
export function mockProject(p: Project) {
  server.use(
    http.get('*/api/v1/projects/:id', ({ params }) =>
      params.id === p.id ? HttpResponse.json(p) : HttpResponse.json({ title: 'Not found', code: 'NOT_FOUND', status: 404 }, { status: 404 }),
    ),
    http.get('*/api/v1/projects', () => HttpResponse.json({ items: [p], nextCursor: '' })),
  )
}
