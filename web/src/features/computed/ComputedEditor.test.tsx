import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import axe from 'axe-core'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { Inspector } from '@/features/mapping/Inspector'
import { MappingCanvas } from '@/features/mapping/MappingCanvas'
import { setComputed, valueSourceOf } from '@/features/mapping/mappingOps'
import type { MappingPreview } from '@/features/mapping/types'
import { mockPreview, renderWorkbench } from '@/test/mapping'
import { withFixedLayout } from '@/test/layout'
import { server, setupMswServer } from '@/test/msw'
import { mockSession } from '@/test/session'
import { valuesAt } from './ComputedEditor'

setupMswServer()
beforeEach(() => mockSession('owner'))

const docs = (ages: number[]): MappingPreview => ({
  writeMode: 'upsert',
  items: ages.map((age, i) => ({ sourceRow: {}, sizeBytes: 10, warnings: [], error: null, document: { customerId: i + 1, age, label: `#${i + 1}` } })),
})

describe('Computed fields in the Inspector', () => {
  it('OC-138 AC1: Source, Fixed and Template; leaving a mapped source asks once', async () => {
    const user = userEvent.setup()
    mockPreview(docs([30]))
    const { store } = renderWorkbench(<Inspector projectId="p1" />)
    // f_age has no mapping: Template at once.
    act(() => store.getState().select({ kind: 'field', id: 'f_age' }))
    const group = screen.getByRole('radiogroup', { name: 'Value source' })
    expect(within(group).getByRole('radio', { name: 'Source' })).toHaveAttribute('aria-checked', 'true')
    await user.click(within(group).getByRole('radio', { name: 'Template' }))
    expect(valueSourceOf(store.getState().history.present, 'f_age')).toBe('template')
    expect(screen.getByRole('textbox', { name: 'Template' })).toBeInTheDocument()

    // f_id is mapped from customer_id: switching asks first, and undo restores the mapping.
    act(() => store.getState().select({ kind: 'field', id: 'f_id' }))
    await user.click(within(screen.getByRole('radiogroup', { name: 'Value source' })).getByRole('radio', { name: 'Template' }))
    const confirm = await screen.findByRole('dialog', { name: 'Stop mapping from source?' })
    expect(confirm).toHaveTextContent('removes the mapping from customer_id')
    await user.click(within(confirm).getByRole('button', { name: 'Remove the mapping' }))
    expect(valueSourceOf(store.getState().history.present, 'f_id')).toBe('template')
    act(() => store.getState().undo())
    expect(store.getState().history.present.fieldMappings.find((m) => m.targetFieldId === 'f_id')!.sources).toEqual([{ column: 'customer_id' }])
  })

  it('OC-138 AC3/AC4: completions by keyboard, the text is saved, the value shows for each sample', async () => {
    const user = userEvent.setup()
    const bodies = mockPreview(docs([30, 41]))
    const view = (await import('@/test/mapping')).mappingView()
    view.spec = setComputed(view.spec, 'f_age', { lang: 'template', expr: '' })
    const { store } = renderWorkbench(<Inspector projectId="p1" />, view)
    act(() => store.getState().select({ kind: 'field', id: 'f_age' }))
    const editor = screen.getByRole('textbox', { name: 'Template' })
    // "Age ${doc.cu" (user-event types {{ as a literal {) → customerId suggested.
    await user.type(editor, 'Age ${{doc.cu')
    const list = await screen.findByRole('listbox', { name: 'Fields you can use' })
    expect(within(list).getAllByRole('option').map((o) => o.textContent)).toEqual(['customerIdinteger'])
    await user.keyboard('{Enter}}')
    expect(editor).toHaveValue('Age ${doc.customerId}')
    await waitFor(() => expect(store.getState().history.present.fieldMappings.find((m) => m.targetFieldId === 'f_age')!.computed!.expr).toBe('Age ${doc.customerId}'))
    // The preview runs with the text as typed, and the value is read at the field's path.
    await waitFor(() => expect(bodies.some((b) => (b.specOverride?.fieldMappings ?? []).some((m) => m.computed?.expr === 'Age ${doc.customerId}'))).toBe(true))
    const result = screen.getByRole('region', { name: 'Value for the sample documents' })
    await waitFor(() => expect(result).toHaveTextContent('→ 30'))
    await user.click(within(result).getByRole('button', { name: /Next/ }))
    expect(result).toHaveTextContent('→ 41')
  })

  it('a template written like code offers the template it means', async () => {
    const user = userEvent.setup()
    mockPreview(docs([1]))
    const view = (await import('@/test/mapping')).mappingView()
    view.spec = setComputed(view.spec, 'f_age', { lang: 'template', expr: '' })
    const { store } = renderWorkbench(<Inspector projectId="p1" />, view)
    act(() => store.getState().select({ kind: 'field', id: 'f_age' }))
    const editor = screen.getByRole('textbox', { name: 'Template' })
    await user.type(editor, '"paid_in_out::" + ${{doc.customerId}')
    await user.keyboard('{Escape}')
    expect(screen.getByText(/the quotes and \+ would be in it/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Use paid_in_out::${doc.customerId}' }))
    expect(editor).toHaveValue('paid_in_out::${doc.customerId}')
    expect(screen.queryByText(/the quotes and \+ would be in it/)).toBeNull()
  })

  it("the server's reason shows when the preview can't run", async () => {
    const user = userEvent.setup()
    server.use(
      http.post('*/api/v1/projects/:pid/mappings/:mid/preview', () =>
        HttpResponse.json({ status: 422, code: 'MAPPING_INVALID', title: 'Invalid mapping', fieldErrors: [{ field: 'age', message: 'age reads itself. To read the source row\'s age column, write ${row.age}.' }] }, { status: 422 }),
      ),
    )
    const view = (await import('@/test/mapping')).mappingView()
    view.spec = setComputed(view.spec, 'f_age', { lang: 'template', expr: '' })
    const { store } = renderWorkbench(<Inspector projectId="p1" />, view)
    act(() => store.getState().select({ kind: 'field', id: 'f_age' }))
    await user.type(screen.getByRole('textbox', { name: 'Template' }), 'x')
    expect(await screen.findByRole('alert')).toHaveTextContent('age reads itself. To read the source row\'s age column, write ${row.age}.')
  })

  it('a syntax error shows at once', async () => {
    const user = userEvent.setup()
    mockPreview(docs([1]))
    const view = (await import('@/test/mapping')).mappingView()
    view.spec = setComputed(view.spec, 'f_age', { lang: 'template', expr: '' })
    const { store } = renderWorkbench(<Inspector projectId="p1" />, view)
    act(() => store.getState().select({ kind: 'field', id: 'f_age' }))
    await user.type(screen.getByRole('textbox', { name: 'Template' }), 'x ${{doc.age + 1}')
    expect(screen.getByRole('alert')).toHaveTextContent('A template only reads values')
  })
})

describe('Uses and used by', () => {
  it('OC-139 AC4: links to the fields it reads and the computed fields reading it', async () => {
    const user = userEvent.setup()
    mockPreview(docs([1]))
    const view = (await import('@/test/mapping')).mappingView()
    view.spec = setComputed(view.spec, 'f_age', { lang: 'template', expr: '${doc.customerId}' })
    view.spec = setComputed(view.spec, 'f_email', { lang: 'template', expr: 'u${doc.age}' })
    const { store } = renderWorkbench(<Inspector projectId="p1" />, view)
    act(() => store.getState().select({ kind: 'field', id: 'f_age' }))
    const inspector = screen.getByRole('complementary', { name: 'Inspector' })
    expect(inspector).toHaveTextContent('Uses:customerId·Used by:email')
    await user.click(within(inspector).getByRole('button', { name: 'email' }))
    expect(store.getState().selection).toEqual({ kind: 'field', id: 'f_email' })
  })
})

describe('Accessibility', () => {
  it('OC-138 AC6: the value source is a radio group, the editor a textbox with a listbox, no axe violations', async () => {
    mockPreview(docs([1]))
    const view = (await import('@/test/mapping')).mappingView()
    view.spec = setComputed(view.spec, 'f_age', { lang: 'template', expr: '${doc.customerId}' })
    const { store } = renderWorkbench(<Inspector projectId="p1" />, view)
    act(() => store.getState().select({ kind: 'field', id: 'f_age' }))
    const inspector = screen.getByRole('complementary', { name: 'Inspector' })
    // Source, Fixed, Template, and Now (OC-179) on a field that can hold a date.
    expect(within(inspector).getAllByRole('radio').map((r) => r.textContent)).toEqual(['Source', 'Fixed', 'Template', 'Date'])
    const res = await axe.run(inspector, { rules: { 'color-contrast': { enabled: false } } })
    expect(res.violations).toEqual([])
  })
})

describe('Add computed field', () => {
  withFixedLayout(320, 600)
  it('OC-138 AC2: ƒx adds a template field, selected, with the ƒx badge', async () => {
    const user = userEvent.setup()
    const { store } = renderWorkbench(<MappingCanvas />)
    await user.click(screen.getByRole('button', { name: 'Add computed field' }))
    const spec = store.getState().history.present
    const added = spec.docSchema.root.children!.find((c) => c.name === 'computedField')!
    expect(valueSourceOf(spec, added.id)).toBe('template')
    expect(store.getState().selection).toEqual({ kind: 'field', id: added.id })
    expect(screen.getByTitle(/^Computed:/)).toHaveTextContent('ƒx')
  })
})

describe('valuesAt', () => {
  it('reads a path, each item’s for arrays', () => {
    const doc = { a: 1, items: [{ x: 'p' }, { x: 'q' }, {}], o: { y: true } }
    expect(valuesAt(doc, 'a')).toEqual([1])
    expect(valuesAt(doc, 'items[].x')).toEqual(['p', 'q'])
    expect(valuesAt(doc, 'o.y')).toEqual([true])
    expect(valuesAt(doc, 'nope')).toEqual([])
  })
})
