import { screen, within } from '@testing-library/react'
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

setupMswServer()

const summary = (over: Partial<MappingSummary>): MappingSummary => ({
  id: 'm1', sourceSchema: 'sales', sourceTable: 'orders', mode: 'canvas', driving: { schema: 'sales', name: 'orders' },
  target: { bucket: 'shop', scope: 'sales', collection: 'orders' }, location: { kind: 'couchbase', bucket: 'shop', scope: 'sales', collection: 'orders' },
  status: 'ready', stats: stats(), version: 1, updatedAt: new Date().toISOString(), shape: { arrays: 0, objects: 0 },
  ...over,
})

beforeEach(() => {
  mockSession('owner')
  mockProject(makeProject({ source: repo('postgres'), target: repo('couchbase'), mappings: 2, readyMappings: 2 }))
  server.use(
    http.get('*/api/v1/projects/:pid/mappings', () =>
      HttpResponse.json({
        items: [
          summary({}),
          summary({ id: 'm2', sourceTable: 'order_detail', mode: 'composed', driving: { schema: 'sales', name: 'order_detail' }, shape: { arrays: 2, objects: 1, groupBy: ['order_id'] } }),
        ],
      }),
    ),
  )
})

describe('MappingsStep', () => {
  it('AC3: open in a view', async () => {
    renderRoutes(routes, '/projects/p1/mappings')
    const detail = (await screen.findByRole('link', { name: 'Open mapping for order_detail' }, { timeout: 5000 })).closest('li')!
    expect(within(detail).getByText('Composed')).toBeInTheDocument()
    expect(within(detail).getByTestId('shape')).toHaveTextContent('nested: 2 arrays, 1 object · one per distinct order_id')
    const orders = screen.getByRole('link', { name: 'Open mapping for orders' }).closest('li')!
    expect(within(orders).getByTestId('shape')).toHaveTextContent('flat document')

    await userEvent.click(within(detail).getByRole('button', { name: 'Open order_detail in…' }))
    const items = await screen.findAllByRole('menuitem')
    expect(items.map((i) => i.textContent)).toEqual(['Open in Canvas', 'Open in Composer', 'Open in Design'])
    expect(items[1]).toHaveAttribute('href', '/projects/p1/mappings/m2?view=composer')
  })
})
