import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { MappingSummary } from '@/features/mapping/types'
import { cartDesign, cartProposal, cartView, mockPOS } from '@/test/designFirst'
import { mockMapping, stats } from '@/test/mapping'
import { server, setupMswServer } from '@/test/msw'
import { makeProject, mockProject, repo } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'

setupMswServer()

const carts = { schema: 'pos', name: 'carts' }
const summary = (over: Partial<MappingSummary>): MappingSummary => ({
  id: 'm1', sourceSchema: 'pos', sourceTable: 'carts', mode: 'canvas', driving: carts,
  target: { bucket: 'shop', scope: 'pos', collection: 'carts' }, location: { kind: 'couchbase', bucket: 'shop', scope: 'pos', collection: 'carts' },
  status: 'ready', stats: stats({ mappedTargetLeaves: 34, totalTargetLeaves: 48 }), version: 3, updatedAt: '2026-10-16T12:00:00Z', shape: { arrays: 0, objects: 0 },
  errors: 0, warnings: 0, keyTemplate: 'cart::{id}', createdBy: 'ana@example.com',
  ...over,
})

function serve(items: MappingSummary[]) {
  const calls = { creates: [] as unknown[], puts: 0 }
  mockPOS()
  server.use(
    http.get('*/api/v1/projects/:pid/mappings', () => HttpResponse.json({ items })),
    http.post('*/api/v1/doc-schemas/infer', () => HttpResponse.json({ docSchema: cartDesign, warnings: [] })),
    http.post('*/api/v1/projects/:pid/mappings/propose', () => HttpResponse.json(cartProposal)),
  )
  return calls
}

async function chooseDesign() {
  renderRoutes(routes, '/projects/p1/mappings')
  await userEvent.click(await screen.findByRole('button', { name: 'New mapping from a design…' }))
  const dialog = await screen.findByRole('dialog', { name: 'New mapping from a design' })
  await userEvent.click(within(dialog).getByRole('textbox', { name: 'The JSON design' }))
  await userEvent.paste('{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"id":{"type":"string"}}}')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Find the table' }))
  expect(await within(dialog).findByRole('radio', { name: 'pos.carts' })).toBeChecked()
  return dialog
}

beforeEach(() => {
  window.sessionStorage.clear()
  mockSession('owner')
  mockProject(makeProject({ source: repo('postgres'), target: repo('couchbase'), mappings: 1, readyMappings: 1 }))
})

describe('New mapping from a design (OC-163, OC-170)', () => {
  it('OC-163 AC3: creates a composed mapping of the proposed table and opens the review', async () => {
    serve([summary({ id: 'm9', sourceTable: 'stores', driving: { schema: 'pos', name: 'stores' } })])
    const created = mockMapping(cartView({ id: 'm2', version: 1 }))
    let body: Record<string, unknown> | undefined
    server.use(
      http.post('*/api/v1/projects/:pid/mappings', async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>
        return HttpResponse.json(created.view, { status: 201 })
      }),
    )
    const dialog = await chooseDesign()
    expect(dialog).toHaveTextContent('key: cart::{uuid} ↔ carts.id')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Create and propose' }))
    await waitFor(() => expect(body).toMatchObject({ sourceSchema: 'pos', sourceTable: 'carts', target: { bucket: 'shop', scope: 'pos', collection: 'carts' } }))
    await waitFor(() => expect(created.puts.length).toBeGreaterThan(0))
    expect(created.puts[0]!.body).toMatchObject({ spec: { mode: 'composed', root: { table: carts } } })
    expect(await screen.findByRole('region', { name: 'Proposed mapping' }, { timeout: 5000 })).toBeInTheDocument()
  })

  it('OC-170 AC1/AC2: an already mapped table asks first, with the mapping, and updates it through the import', async () => {
    serve([summary({})])
    mockMapping(cartView({ id: 'm1', version: 3 }))
    const dialog = await chooseDesign()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Create and propose' }))
    const asked = await screen.findByRole('dialog', { name: 'pos.carts is already mapped' })
    const card = within(asked).getByRole('article', { name: 'Mapping of carts' })
    expect(card).toHaveTextContent('shop.pos.carts')
    expect(card).toHaveTextContent('cart::{id}')
    expect(card).toHaveTextContent('34 of 48 fields')
    expect(card).toHaveTextContent('created by ana@example.com')
    // A project maps each table once: only update (or cancel).
    expect(within(asked).getByRole('button', { name: 'Create a new mapping' })).toBeDisabled()
    expect(asked).toHaveTextContent('A project maps each table once')
    await userEvent.click(within(card).getByRole('button', { name: 'Update this mapping' }))
    // The mapping's import opens with the design, proposing what's missing after the update.
    const imp = await screen.findByRole('dialog', { name: /Import/ }, { timeout: 5000 })
    expect(within(imp).getByRole('textbox')).toHaveValue('{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"id":{"type":"string"}}}')
    expect(within(imp).getByRole('checkbox', { name: /Propose the mapping from the source/ })).toBeChecked()
  })

  it('OC-170 AC3: a new mapping of the same table writing the same keys warns', async () => {
    // Driven by pos.carts, on another table: a second mapping of carts is allowed.
    serve([summary({ id: 'm5', sourceTable: 'cart_view', mode: 'composed' })])
    const dialog = await chooseDesign()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Create and propose' }))
    const asked = await screen.findByRole('dialog', { name: 'pos.carts is already mapped' })
    expect(within(asked).getByRole('alert')).toHaveTextContent('Both mappings write cart::{id} keys into shop.pos.carts')
    expect(within(asked).getByRole('button', { name: 'Create a new mapping' })).toBeEnabled()
    await userEvent.click(within(asked).getByRole('button', { name: 'Back' }))
    const coll = within(screen.getByRole('dialog')).getByRole('textbox', { name: 'Target collection' })
    await userEvent.clear(coll)
    await userEvent.type(coll, 'carts_v2')
    await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Create and propose' }))
    expect(within(await screen.findByRole('dialog', { name: 'pos.carts is already mapped' })).queryByRole('alert')).not.toBeInTheDocument()
  })

  it('OC-170 AC4: Cancel changes nothing; a viewer cannot start it', async () => {
    serve([summary({})])
    let created = false
    server.use(http.post('*/api/v1/projects/:pid/mappings', () => ((created = true), HttpResponse.json({}, { status: 500 }))))
    const dialog = await chooseDesign()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Create and propose' }))
    const asked = await screen.findByRole('dialog', { name: 'pos.carts is already mapped' })
    await userEvent.click(within(asked).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(created).toBe(false)
  })

  it('OC-170 AC4: a viewer cannot start it', async () => {
    mockSession('viewer')
    serve([summary({})])
    renderRoutes(routes, '/projects/p1/mappings')
    await screen.findByRole('link', { name: 'Open mapping for carts' }, { timeout: 5000 })
    await waitFor(() => expect(screen.getByRole('button', { name: 'New mapping from a design…' })).toBeDisabled())
  })
})
