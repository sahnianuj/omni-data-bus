import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { http, HttpResponse } from 'msw'
import { mappingView, mockPreview, renderWorkbench, sampleSpec, scol } from '@/test/mapping'
import { server, setupMswServer } from '@/test/msw'
import { mockSession } from '@/test/session'
import { Inspector } from './Inspector'
import type { MappingPreview } from './types'

setupMswServer()
beforeEach(() => mockSession('owner'))

const preview = (after: string[]): MappingPreview => ({
  writeMode: 'upsert',
  fields: [' Ada ', 'Linus', 'Grace '].map((before, i) => ({ before, after: after[i], steps: [] })),
})

describe('Inspector', () => {
  it('AC8: shows sources, explain, preview', async () => {
    const bodies = mockPreview(preview(['ADA', 'LINUS', 'GRACE']))
    const { store } = renderWorkbench(<Inspector projectId="p1" />)
    act(() => store.getState().select({ kind: 'mapping', id: 'm_id' }))

    const inspector = screen.getByRole('complementary', { name: 'Inspector' })
    expect(within(inspector).getByRole('heading', { level: 3 })).toHaveTextContent('customer_id→customerId')
    const why = within(inspector).getByRole('region', { name: 'Why this match' })
    expect(why).toHaveTextContent('Names match ignoring case and separators (97%); types match')
    expect(why).toHaveTextContent('NORMALIZED_EQUAL +0.97')

    const pv = within(inspector).getByRole('region', { name: 'Preview' })
    await waitFor(() => expect(within(pv).getAllByRole('listitem')).toHaveLength(3))
    expect(within(pv).getAllByRole('listitem')[0]).toHaveTextContent('" Ada "→"ADA"')
    expect(bodies).toHaveLength(1)
    expect(bodies[0]).toMatchObject({ fieldMappingId: 'm_id', limit: 3 })

    // A suggested mapping explains itself but has no preview until accepted.
    act(() => store.getState().select({ kind: 'mapping', id: 'm_email' }))
    expect(within(inspector).getByRole('region', { name: 'Why this match' })).toHaveTextContent('SYNONYM +0.88')
    expect(within(inspector).queryByRole('region', { name: 'Preview' })).not.toBeInTheDocument()
  })

  describe('with fake timers', () => {
    beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }))
    afterEach(() => vi.useRealTimers())

    it('AC8: debounced preview refresh on transform change', async () => {
      const bodies = mockPreview(preview(['Ada', 'Linus', 'Grace']))
      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
      const { store } = renderWorkbench(<Inspector projectId="p1" />)
      act(() => store.getState().select({ kind: 'mapping', id: 'm_first' }))
      await waitFor(() => expect(bodies).toHaveLength(1))

      // Two quick edits: nothing is sent until 300 ms after the last one.
      await user.click(screen.getByRole('button', { name: 'Add transform' }))
      await user.click(screen.getByRole('button', { name: 'Uppercase' }))
      await user.click(within(screen.getByRole('listitem', { name: 'Step 2: Uppercase' })).getByRole('button', { name: 'Move up' }))
      act(() => vi.advanceTimersByTime(200))
      expect(bodies).toHaveLength(1)

      act(() => vi.advanceTimersByTime(110))
      await waitFor(() => expect(bodies).toHaveLength(2))
      const override = bodies[1]!.specOverride!.fieldMappings.find((m) => m.id === 'm_first')!
      expect(override.transforms).toEqual([{ op: 'upper' }, { op: 'trim' }])
    })
  })

  it('OC-143 AC2: the Canvas embeds a foreign-key field and collapses it back', async () => {
    const user = userEvent.setup()
    const automap: unknown[] = []
    server.use(
      http.get('*/api/v1/connections/:id/relations/hops', ({ request }) =>
        HttpResponse.json({
          table: { schema: 'shop', name: 'customer' }, junctions: [], advice: [], maxRelationDepth: 4,
          hops: new URL(request.url).searchParams.get('table') === 'shop.customer'
            ? [{ from: { schema: 'shop', name: 'customer' }, to: { schema: 'shop', name: 'address' }, kind: 'many-to-one', join: [{ parent: 'address_id', child: 'address_id' }], indexed: true }]
            : [],
        })),
      http.get('*/api/v1/connections/:id/schema/shop/address', () => HttpResponse.json({ name: 'address', columns: [scol('address_id', 'integer', { isPrimaryKey: true }), scol('city', 'string')] })),
      http.post('*/api/v1/projects/:pid/mappings/:mid/automap', async ({ request }) => {
        automap.push(await request.json())
        return HttpResponse.json({ status: 500, code: 'X', title: 'not in this test' }, { status: 500 })
      }),
    )
    const spec = sampleSpec()
    spec.docSchema.root.children = [...(spec.docSchema.root.children ?? []), { id: 'f_addr', name: 'addressId', type: 'integer' }]
    spec.fieldMappings = [...spec.fieldMappings, { id: 'm_addr', targetFieldId: 'f_addr', sources: [{ column: 'address_id' }], transforms: [], origin: 'manual', state: 'active' }]
    const { store } = renderWorkbench(<Inspector projectId="p1" />, mappingView({ spec }))
    act(() => store.getState().select({ kind: 'field', id: 'f_addr' }))

    await user.click(await screen.findByRole('button', { name: 'Embed address here…' }))
    const dialog = await screen.findByRole('dialog', { name: 'Embed address' })
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Embed' })).toBeEnabled())
    await user.click(within(dialog).getByRole('button', { name: 'Embed' }))
    const after = store.getState().history.present
    const rel = after.relations.find((r) => r.table.name === 'address')!
    expect(rel).toMatchObject({ kind: 'many-to-one', join: [{ parent: 'address_id', child: 'address_id' }] })
    expect(after.docSchema.root.children!.some((c) => c.id === 'f_addr')).toBe(false) // replaced by the object (OC-082)
    await waitFor(() => expect(automap).toEqual([{ mode: 'fill', nodeId: rel.targetFieldId }]))

    // Collapse to id restores the key field.
    act(() => store.getState().select({ kind: 'field', id: rel.targetFieldId! }))
    await user.click(await screen.findByRole('button', { name: 'Collapse to id' }))
    const back = store.getState().history.present
    expect(back.relations.some((r) => r.table.name === 'address')).toBe(false)
    expect(back.fieldMappings.find((m) => m.targetFieldId === rel.targetFieldId)!.sources).toEqual([{ column: 'address_id' }])
  })

  it('OC-143 AC3: at the depth limit the action shows, disabled, with why', async () => {
    server.use(
      http.get('*/api/v1/connections/:id/relations/hops', ({ request }) => {
        const table = new URL(request.url).searchParams.get('table')
        return HttpResponse.json({
          table: { schema: 'shop', name: 'x' }, junctions: [], advice: [], maxRelationDepth: 1,
          hops: table === 'shop.address'
            ? [{ from: { schema: 'shop', name: 'address' }, to: { schema: 'shop', name: 'country' }, kind: 'many-to-one', join: [{ parent: 'country_code', child: 'code' }], indexed: true }]
            : [],
        })
      }),
    )
    const spec = sampleSpec()
    // profile is the customer's address (depth 1), holding countryCode ← address.country_code.
    spec.docSchema.root.children = spec.docSchema.root.children!.map((c) => (c.id === 'f_profile' ? { ...c, children: [{ id: 'f_cc', name: 'countryCode', type: 'string' as const }] } : c))
    spec.relations = [{ id: 'r_a', parentId: null, kind: 'many-to-one', table: { schema: 'shop', name: 'address' }, join: [{ parent: 'address_id', child: 'address_id' }], targetFieldId: 'f_profile' }]
    spec.fieldMappings = [...spec.fieldMappings, { id: 'm_cc', targetFieldId: 'f_cc', sources: [{ relation: 'r_a', column: 'country_code' }], transforms: [], origin: 'manual', state: 'active' }]
    const { store } = renderWorkbench(<Inspector projectId="p1" />, mappingView({ spec }))
    act(() => store.getState().select({ kind: 'field', id: 'f_cc' }))
    const button = await screen.findByRole('button', { name: 'Embed country here…' })
    expect(button).toBeDisabled()
    expect(button).toHaveAttribute('title', 'Deepest level reached (1 of 1). An administrator can raise OC_MAX_RELATION_DEPTH up to 6.')
  })

  it('OC-178 AC3: a field nothing fills can be marked not populated, and mapped again', async () => {
    const { store } = renderWorkbench(<Inspector projectId="p1" />)
    act(() => store.getState().select({ kind: 'field', id: 'f_age' }))
    const before = store.getState().stats().targetFieldsDone
    const inspector = screen.getByRole('complementary', { name: 'Inspector' })
    await userEvent.click(within(inspector).getByRole('button', { name: 'Mark not populated' }))
    expect(store.getState().stats().targetFieldsDone).toBe(before + 1)
    expect(within(inspector).getByRole('heading', { level: 3 })).toHaveTextContent('Not populated→age')
    expect(within(inspector).getByRole('note')).toHaveTextContent('leaves this field empty on purpose')
    await userEvent.click(within(inspector).getByRole('button', { name: 'Map it instead' }))
    expect(store.getState().stats().targetFieldsDone).toBe(before)
    act(() => store.getState().select({ kind: 'field', id: 'f_id' }))
    expect(within(inspector).queryByRole('button', { name: 'Mark not populated' })).toBeNull()
  })

  it('OC-179 AC1: Now fills a field with the current date and time, like the other dates', async () => {
    const { store } = renderWorkbench(<Inspector projectId="p1" />)
    act(() => store.getState().select({ kind: 'field', id: 'f_age' }))
    const inspector = screen.getByRole('complementary', { name: 'Inspector' })
    await userEvent.click(within(inspector).getByRole('radio', { name: 'Date' }))
    // createdAt is written as epoch ms: age (an integer) takes the same.
    const format = within(inspector).getByRole('combobox', { name: 'Format' })
    expect(within(format).getByRole('option', { selected: true })).toHaveTextContent('Same as createdAt (epoch ms)')
    expect(within(inspector).getByTestId('now-sample').textContent).toMatch(/^\d{13}$/)
    expect(within(inspector).getByRole('heading', { level: 3 })).toHaveTextContent('Current date and time→age')
    expect(store.getState().history.present.fieldMappings.find((m) => m.targetFieldId === 'f_age')).toMatchObject({ sources: [], transforms: [{ op: 'now', args: { at: 'run', format: 'epoch_ms' } }] })

    // Each document's write time, as ISO 8601: text into an integer is flagged.
    await userEvent.click(within(inspector).getByRole('radio', { name: 'When each document is written' }))
    await userEvent.selectOptions(format, 'iso8601')
    expect(within(inspector).getByRole('alert')).toHaveTextContent('age is integer: use an epoch format.')
    expect(within(inspector).getByTestId('now-sample').textContent).toMatch(/^"\d{4}-\d{2}-\d{2}T/)
    expect(store.getState().history.present.fieldMappings.find((m) => m.targetFieldId === 'f_age')!.transforms).toEqual([{ op: 'now', args: { at: 'write', format: 'iso8601' } }])
  })

  it('Source shows how to map a column; Fixed shows the value box', async () => {
    const { store } = renderWorkbench(<Inspector projectId="p1" />)
    act(() => store.getState().select({ kind: 'field', id: 'f_age' }))
    const inspector = screen.getByRole('complementary', { name: 'Inspector' })
    expect(within(inspector).getByText(/Drag a source column onto this field/)).toBeInTheDocument()
    expect(within(inspector).queryByRole('textbox', { name: 'Constant value' })).toBeNull()
    await userEvent.click(within(inspector).getByRole('radio', { name: 'Fixed' }))
    expect(within(inspector).getByRole('radio', { name: 'Fixed' })).toHaveAttribute('aria-checked', 'true')
    expect(within(inspector).queryByText(/Drag a source column onto this field/)).toBeNull()
    await userEvent.type(within(inspector).getByRole('textbox', { name: 'Constant value' }), '42')
    await userEvent.click(within(inspector).getByRole('button', { name: 'Set constant' }))
    expect(store.getState().history.present.fieldMappings.find((m) => m.targetFieldId === 'f_age')).toMatchObject({ sources: [], transforms: [{ op: 'constant', args: { value: 42 } }] })
    // Back to Source: the value box goes.
    await userEvent.click(within(inspector).getByRole('radio', { name: 'Source' }))
    expect(within(inspector).queryByRole('textbox', { name: 'Constant value' })).toBeNull()
  })
})
