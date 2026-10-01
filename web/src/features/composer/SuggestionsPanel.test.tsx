import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import type { BindingSuggestions } from '@/features/mapping/api'
import type { DocField, MappingSpec } from '@/features/mapping/types'
import { composedSpec, composedView, mockComposer } from '@/test/composer'
import { renderWorkbench } from '@/test/mapping'
import { server, setupMswServer } from '@/test/msw'
import { mockSession } from '@/test/session'
import { nodeOf } from './composerOps'
import { SuggestionsPanel } from './SuggestionsPanel'

setupMswServer()

/** The composer fixture's design as imported: nothing bound or mapped. */
function unboundSpec(): MappingSpec {
  const strip = (f: DocField): DocField => {
    const rest = { ...f }
    delete rest.binding
    return { ...rest, ...(f.children ? { children: f.children.map(strip) } : {}), ...(f.items ? { items: strip(f.items) } : {}) }
  }
  const s = composedSpec()
  return { ...s, relations: [], aggregates: [], fieldMappings: [], docSchema: { ...s.docSchema, root: strip(s.docSchema.root) } }
}

const t = (name: string) => ({ schema: 'public', name })
const suggestions: BindingSuggestions = {
  root: [{ table: t('orders'), score: 0.95, reasons: ['fields 2/2'] }],
  suggestions: [
    { nodeId: 'f_cust', path: 'customer', confidence: 'high', candidate: { kind: 'many-to-one', table: t('customers'), join: [{ parent: 'customer_id', child: 'customer_id' }], score: 0.98, reasons: ['name: customer ~ customers (0.93)', 'fields 1/1', 'many-to-one ✓'] }, alternatives: [] },
    {
      nodeId: 'f_items', path: 'items[]', confidence: 'high',
      candidate: { kind: 'one-to-many', table: t('order_items'), join: [{ parent: 'order_id', child: 'order_id' }], score: 0.97, reasons: ['name: items ~ order_items (0.95)', 'fields 2/2', 'one-to-many ✓'] },
      alternatives: [{ kind: 'many-to-many', table: t('products'), via: { table: t('order_items'), parentJoin: [{ parent: 'order_id', via: 'order_id' }], childJoin: [{ via: 'product_id', child: 'product_id' }] }, score: 0.5, reasons: ['fields 1/2'] }],
    },
    { nodeId: 'f_product', path: 'items[].product', confidence: 'high', candidate: { kind: 'many-to-one', table: t('products'), join: [{ parent: 'product_id', child: 'product_id' }], score: 0.98, reasons: ['fields 1/1'] }, alternatives: [] },
    { nodeId: 'f_tags', path: 'tags[]', confidence: 'medium', candidate: { kind: 'many-to-many', table: t('tags'), via: { table: t('order_tags'), parentJoin: [{ parent: 'order_id', via: 'order_id' }], childJoin: [{ via: 'tag_id', child: 'tag_id' }] }, score: 0.62, reasons: ['fields 1/1'] }, alternatives: [] },
  ],
}

let bodies: { root?: boolean; specOverride?: MappingSpec }[] = []
beforeEach(() => {
  mockSession('owner')
  mockComposer()
  bodies = []
  server.use(
    http.post('*/api/v1/projects/:pid/mappings/:mid/suggest-bindings', async ({ request }) => {
      bodies.push((await request.json()) as never)
      return HttpResponse.json(suggestions)
    }),
  )
})

function renderPanel() {
  const calls = { close: 0, automap: 0, other: [] as unknown[] }
  const res = renderWorkbench(
    <SuggestionsPanel onClose={() => calls.close++} onAutoMapAll={() => calls.automap++} onOther={(...a) => calls.other.push(a)} />,
    composedView({ spec: unboundSpec() }),
  )
  return { ...res, calls }
}

describe('SuggestionsPanel', () => {
  it('AC6: accept all high', async () => {
    const { store, calls } = renderPanel()
    const list = await screen.findByRole('list', { name: 'Suggestions' })
    expect(within(list).getAllByRole('listitem').map((li) => li.getAttribute('aria-label'))).toEqual(['customer', 'items[]', 'items[].product', 'tags[]'])
    expect(bodies[0]).toMatchObject({ root: true, specOverride: unboundSpec() }) // unsaved edits included

    await userEvent.click(screen.getByRole('button', { name: 'Accept high & auto-map' }))
    const spec = store.getState().history.present
    // Three bindings, parents first: product nests under items' relation.
    expect(spec.relations.map((r) => [r.targetFieldId, r.table.name, r.kind])).toEqual([
      ['f_cust', 'customers', 'many-to-one'],
      ['f_items', 'order_items', 'one-to-many'],
      ['f_product', 'products', 'many-to-one'],
    ])
    const items = spec.relations.find((r) => r.targetFieldId === 'f_items')!
    expect(spec.relations.find((r) => r.targetFieldId === 'f_product')!.parentId).toBe(items.id)
    expect(nodeOf(spec, 'f_items')!.binding).toEqual({ relationId: items.id })
    expect(spec.relations.some((r) => r.targetFieldId === 'f_tags')).toBe(false) // medium: not accepted
    expect(calls).toMatchObject({ close: 1, automap: 1 })
  })

  it('accepts one, uses an alternative, rejects, or hands over to the Bind dialog', async () => {
    const { store, calls } = renderPanel()
    await screen.findByRole('list', { name: 'Suggestions' })
    await userEvent.click(screen.getByRole('button', { name: 'Accept the suggestion for tags[]' }))
    expect(store.getState().history.present.relations.map((r) => [r.targetFieldId, r.kind, r.via?.table.name])).toEqual([['f_tags', 'many-to-many', 'order_tags']])

    await userEvent.click(screen.getByRole('button', { name: /^Use public\.products · many-to-many .* for items\[\]$/ }))
    expect(store.getState().history.present.relations.at(-1)).toMatchObject({ targetFieldId: 'f_items', kind: 'many-to-many', table: t('products') })
    await waitFor(() => expect(bodies.length).toBeGreaterThan(1)) // recomputed after each change

    await userEvent.click(screen.getByRole('button', { name: 'Reject the suggestion for customer' }))
    expect(within(screen.getByRole('list', { name: 'Suggestions' })).queryByRole('listitem', { name: 'customer' })).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Choose another binding for items[].product' }))
    expect(calls.other).toEqual([['f_product', t('products')]])
  })

  it('offers a better driving table', async () => {
    server.use(http.post('*/api/v1/projects/:pid/mappings/:mid/suggest-bindings', () => HttpResponse.json({ ...suggestions, root: [{ table: t('order_items'), score: 0.9, reasons: ['fields 2/2'] }, { table: t('orders'), score: 0.5, reasons: ['fields 1/2'] }] })))
    const { store } = renderPanel()
    const status = await screen.findByText(/public\.order_items matches the root's fields better/)
    await userEvent.click(within(status.closest('[role="status"]') as HTMLElement).getByRole('button', { name: 'Drive from it' }))
    expect(store.getState().history.present.root).toEqual({ table: t('order_items') })
  })

  it('shows unresolved nodes without an accept', async () => {
    server.use(http.post('*/api/v1/projects/:pid/mappings/:mid/suggest-bindings', () => HttpResponse.json({ root: [], suggestions: [{ nodeId: 'f_cust', path: 'customer', confidence: 'unresolved', candidate: { kind: 'many-to-one', table: t('customers'), score: 0.2, reasons: ['fields 0/1'] }, alternatives: [] }] })))
    renderPanel()
    const item = await screen.findByRole('listitem', { name: 'customer' })
    expect(item).toHaveTextContent('Unresolved')
    expect(item).toHaveTextContent('No table fits well enough')
    expect(within(item).queryByRole('button', { name: /^Accept/ })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Accept high & auto-map' })).toBeDisabled()
  })
})

describe('unbound banner', () => {
  it('counts what an imported design leaves unbound', async () => {
    const { unboundContainers } = await import('./composerOps')
    expect(unboundContainers(unboundSpec())).toEqual({ arrays: 2, objects: 2 })
    expect(unboundContainers(composedSpec())).toEqual({ arrays: 0, objects: 0 })
  })
})
