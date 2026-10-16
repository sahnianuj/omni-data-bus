import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { MappingSummary } from '@/features/mapping/types'
import { mockKeyspaces } from '@/test/keyspaces'
import { mappingView } from '@/test/mapping'
import { server, setupMswServer } from '@/test/msw'
import { renderUI } from '@/test/render'
import { MappingLocations } from './MappingLocations'

setupMswServer()

const summary = (id: string, table: string, bucket: string, scope: string): MappingSummary =>
  ({
    id, sourceSchema: 'public', sourceTable: table, mode: 'canvas', driving: { schema: 'public', name: table },
    target: { bucket, scope, collection: table }, location: { kind: 'couchbase', bucket, scope, collection: table },
    status: 'ready', stats: {}, version: 1, updatedAt: new Date().toISOString(), errors: 0, warnings: 0,
  }) as unknown as MappingSummary

function setup() {
  const list = [summary('m1', 'carts', 'travel-sample', 'inventory'), summary('m2', 'customers', 'tar', 'oe'), summary('m3', 'partners', 'tar', 'oe')]
  const puts: Record<string, { target: unknown }> = {}
  mockKeyspaces()
  server.use(
    http.get('*/api/v1/projects/p1/mappings', () => HttpResponse.json({ items: list })),
    http.get('*/api/v1/projects/p1/mappings/:mid', ({ params }) => {
      const m = list.find((x) => x.id === params.mid)!
      return HttpResponse.json(mappingView({ id: m.id, sourceTable: m.sourceTable, target: m.target, version: 4 }))
    }),
    http.put('*/api/v1/projects/p1/mappings/:mid', async ({ params, request }) => {
      puts[params.mid as string] = (await request.json()) as { target: unknown }
      return HttpResponse.json(mappingView({ id: params.mid as string, version: 5 }))
    }),
  )
  renderUI(<MappingLocations projectId="p1" connId="p1::target" canEdit />)
  return { puts }
}

describe('MappingLocations', () => {
  it('shows the bucket and scope each mapping writes to, and warns when they differ', async () => {
    setup()
    const card = await screen.findByRole('region', { name: 'Where your mappings write' })
    expect(within(card).getByRole('status')).toHaveTextContent('2 different bucket and scope pairs')
    expect(card).toHaveTextContent('bucket tar · scope oe')
    expect(card).toHaveTextContent('bucket travel-sample · scope inventory')
    expect(within(card).getByRole('link', { name: 'public.customers' })).toHaveAttribute('href', '/projects/p1/mappings/m2')
  })

  it('moves a group to another bucket and scope, keeping collection names', async () => {
    const user = userEvent.setup()
    const { puts } = setup()
    await user.click(await screen.findByRole('button', { name: 'Change bucket and scope of tar.oe' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('combobox', { name: 'Bucket' }))
    await user.click(await screen.findByRole('option', { name: /^travel-sample/ }))
    await user.click(within(dialog).getByRole('combobox', { name: 'Scope' }))
    await user.click(await screen.findByRole('option', { name: /^inventory/ }))
    await user.click(within(dialog).getByRole('button', { name: 'Move 2 mappings' }))
    await waitFor(() => expect(Object.keys(puts).sort()).toEqual(['m2', 'm3']))
    expect(puts.m2!.target).toEqual({ bucket: 'travel-sample', scope: 'inventory', collection: 'customers', createIfMissing: true })
    expect(puts.m3!.target).toEqual({ bucket: 'travel-sample', scope: 'inventory', collection: 'partners', createIfMissing: true })
  })
})
