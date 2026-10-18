import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { cartProposal, mockPOS, openCart } from '@/test/designFirst'
import { server, setupMswServer } from '@/test/msw'
import { makeProject, mockProject, repo } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'

setupMswServer()
beforeEach(() => {
  window.localStorage.clear()
  mockSession('owner')
  mockProject(makeProject({ source: repo('postgres'), target: repo('couchbase'), mappings: 1, readyMappings: 0 }))
  server.use(http.get('*/api/v1/projects/:pid/mappings', () => HttpResponse.json({ items: [] })))
})

const tree = () => screen.getByRole('tree', { name: 'Document' })
const row = (label: RegExp) => within(tree()).getByRole('treeitem', { name: label })
const byPath = new Map(cartProposal.items.map((it) => [it.path, it]))

async function proposed() {
  const { proposals } = mockPOS()
  const state = openCart()
  renderRoutes(routes, '/projects/p1/mappings/m1')
  // A large design: give the composer time to load under a full parallel run.
  await userEvent.click(await screen.findByRole('button', { name: 'Propose mapping' }, { timeout: 8000 }))
  const banner = await screen.findByRole('region', { name: 'Proposed mapping' }, { timeout: 8000 })
  return { banner, proposals, state }
}

describe('Review mode (OC-168)', () => {
  it('OC-168 AC1: the proposal on the design tree, with confidence and counts', async () => {
    const { banner, proposals } = await proposed()
    const c = cartProposal.coverage
    expect(within(banner).getByRole('status')).toHaveTextContent(`${c.proposed} of ${c.fields} fields · ${c.needsReview} need review · ${c.notPopulated} not populated · ${c.unresolved} unresolved`)
    expect(banner).toHaveTextContent('from pos.carts')
    expect(proposals[0]).toMatchObject({ base: false })
    expect(row(/^customer, object, bound to customers, high confidence/)).toBeInTheDocument()
    expect(row(/^receipt, object.*not populated/)).toBeInTheDocument()
    const price = within(tree()).getAllByRole('treeitem', { name: /^price, integer, from cart_items\.price, high confidence/ })[0]!
    expect(price).toHaveTextContent('← cart_items.price × 100')
    expect(row(/^updatedAt, datetime, unresolved/)).toBeInTheDocument()
  }, 20_000)

  it('OC-168 AC2 + OC-169 AC2: fine-tune with the editors, filter, and fix what is unresolved', async () => {
    const { banner } = await proposed()
    // Needs review shows the medium rows, not the high ones.
    await userEvent.click(within(banner).getByRole('radio', { name: /Needs review/ }))
    expect(row(/^name, string, .*medium confidence/)).toBeInTheDocument()
    expect(within(tree()).queryByRole('treeitem', { name: /^channel,/ })).not.toBeInTheDocument()

    // partner.name: marked not populated in the Inspector → edited, out of Needs review.
    await userEvent.click(row(/^name, string, .*medium confidence/))
    const panel = await screen.findByRole('region', { name: 'Proposal' })
    expect(panel).toHaveTextContent("no name column: first_name and last_name combined")
    await userEvent.click(within(panel).getByRole('button', { name: 'Mark not populated' }))
    await waitFor(() => expect(within(tree()).queryByRole('treeitem', { name: /^name, string, .*medium confidence/ })).not.toBeInTheDocument())
    await userEvent.click(within(banner).getByRole('radio', { name: /Edited/ }))
    expect(row(/^name, string, .*edited/)).toBeInTheDocument()

    // updatedAt: unresolved, the panel names why and offers the row's unused datetime column.
    await userEvent.click(within(banner).getByRole('radio', { name: /Unresolved/ }))
    await userEvent.click(row(/^updatedAt,/))
    const fix = await screen.findByRole('region', { name: 'Proposal' })
    expect(fix).toHaveTextContent('No column fits this field')
    await userEvent.click(within(fix).getByRole('button', { name: 'Use carts.last_priced_at' }))
    await waitFor(() => expect(within(tree()).queryByRole('treeitem', { name: /^updatedAt, datetime, unresolved/ })).not.toBeInTheDocument())
    await userEvent.click(within(banner).getByRole('radio', { name: /All/ }))
    expect(row(/^updatedAt, datetime, from last_priced_at/)).toHaveAccessibleName(/edited/)
  }, 20_000)

  it('OC-168 AC3: Apply keeps it as one undo step; Discard leaves the design as it was', async () => {
    const { banner, state } = await proposed()
    await userEvent.click(within(banner).getByRole('button', { name: 'Apply' }))
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Proposed mapping' })).not.toBeInTheDocument())
    await waitFor(() => expect(state.view.spec.relations.length).toBe(cartProposal.spec.relations.length))
    expect(row(/^customer, object, bound to customers/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Undo' }))
    await waitFor(() => expect(within(tree()).queryByRole('treeitem', { name: /bound to customers/ })).not.toBeInTheDocument())

    // Again, then Discard.
    await userEvent.click(screen.getByRole('button', { name: 'Propose mapping' }))
    const again = await screen.findByRole('region', { name: 'Proposed mapping' })
    expect(row(/bound to customers/)).toBeInTheDocument()
    await userEvent.click(within(again).getByRole('button', { name: 'Discard' }))
    await waitFor(() => expect(within(tree()).queryByRole('treeitem', { name: /bound to customers/ })).not.toBeInTheDocument())
    expect(screen.getByRole('button', { name: 'Undo' })).toBeDisabled()
  }, 20_000)

  it('OC-168 AC3: Apply high only drops the medium rows and what is under them', async () => {
    const { banner } = await proposed()
    await userEvent.click(within(banner).getByRole('button', { name: 'Apply high only' }))
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Proposed mapping' })).not.toBeInTheDocument())
    expect(byPath.get('items[].modifiers[]')!.band).toBe('medium')
    expect(within(tree()).queryByRole('treeitem', { name: /bound to customizations/ })).not.toBeInTheDocument()
    expect(row(/^customer, object, bound to customers/)).toBeInTheDocument()
  }, 20_000)

  it('OC-168 AC4: n moves to the next row that needs review', async () => {
    await proposed()
    await userEvent.click(within(tree()).getAllByRole('treeitem', { name: /^type, string/ })[0]!)
    await userEvent.keyboard('n')
    const first = cartProposal.items.find((it) => it.path !== 'type' && it.kind && (it.status === 'unresolved' || (it.band !== 'high' && it.status !== 'notPopulated' && it.status !== 'alreadyMapped')))!
    await waitFor(() => expect(within(tree()).getByRole('treeitem', { selected: true })).toHaveAccessibleName(new RegExp('^' + first.path.split('.').at(-1)!.replace('[]', '') + ',')))
  }, 20_000)
})
