import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { DocField, DocSchema } from '@/features/mapping/types'
import { dragOnto } from '@/test/drag'
import { withFixedLayout } from '@/test/layout'
import { mappingView, renderWorkbench, sampleSpec } from '@/test/mapping'
import { server, setupMswServer } from '@/test/msw'
import { DocDesigner } from './DocDesigner'

// Counts DesignRow renders: its root is the only element whose class list starts this way.
const rowRenders = vi.hoisted(() => ({ n: 0 }))
vi.mock('@/lib/utils', async (orig) => {
  const mod = await orig<typeof import('@/lib/utils')>()
  return {
    ...mod,
    cn: (...args: Parameters<typeof mod.cn>) => {
      if (typeof args[0] === 'string' && args[0].startsWith('absolute inset-x-0 flex h-9')) rowRenders.n++
      return mod.cn(...args)
    },
  }
})

setupMswServer()
withFixedLayout(900, 600)
beforeEach(() => {
  rowRenders.n = 0
})

const designer = () => <DocDesigner projectId="p1" targetConnId="c1" />
const row = (name: string) => screen.getAllByRole('treeitem').find((r) => within(r).queryByText(name, { selector: 'span' }))!
const names = (store: ReturnType<typeof renderWorkbench>['store']) => store.getState().history.present.docSchema.root.children!.map((f) => f.name)

describe('DocDesigner', () => {
  it('AC6: keyboard F2 rename and Delete confirm when mapped', async () => {
    const { store } = renderWorkbench(designer())
    const tree = screen.getByRole('tree', { name: 'Document fields' })

    await userEvent.click(row('age'))
    tree.focus()
    await userEvent.keyboard('{F2}')
    const input = screen.getByRole('textbox', { name: 'Rename age' })
    await userEvent.clear(input)
    await userEvent.type(input, 'years{Enter}')
    expect(names(store)).toContain('years')
    expect(store.getState().history.past).toHaveLength(1)

    // Unmapped: Delete removes immediately.
    await userEvent.click(row('years'))
    tree.focus()
    await userEvent.keyboard('{Delete}')
    expect(names(store)).not.toContain('years')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()

    // Mapped: Delete asks first, then drops the field and its mapping.
    await userEvent.click(row('customerId'))
    tree.focus()
    await userEvent.keyboard('{Delete}')
    const dialog = await screen.findByRole('dialog', { name: 'Delete a mapped field?' })
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))
    const spec = store.getState().history.present
    expect(spec.docSchema.root.children!.map((f) => f.name)).not.toContain('customerId')
    expect(spec.fieldMappings.some((m) => m.targetFieldId === 'f_id')).toBe(false)

    store.getState().undo()
    await waitFor(() => expect(row('customerId')).toBeInTheDocument())
  })

  it('fields drag before, after or into an object', () => {
    const { store } = renderWorkbench(designer())
    dragOnto(row('age'), row('customerId'), 0) // rows measure 600 px: top edge is before
    expect(names(store)).toEqual(['age', 'customerId', 'name', 'email', 'createdAt', 'profile'])
    dragOnto(row('customerId'), row('profile'), 590) // bottom edge is after
    expect(names(store)).toEqual(['age', 'name', 'email', 'createdAt', 'profile', 'customerId'])
    dragOnto(row('email'), row('name'), 300) // an object's middle is inside
    expect(store.getState().history.present.docSchema.root.children![1]!.children!.map((f) => f.name)).toEqual(['first', 'last', 'email'])
  })

  it('OC-081: array items and add field', async () => {
    const spec = sampleSpec()
    spec.docSchema.root.children!.push({ id: 'f_items', name: 'items', type: 'array', items: { id: 'f_item', name: '[]', type: 'object', children: [{ id: 'f_sku', name: 'sku', type: 'string' }, { id: 'f_qty', name: 'qty', type: 'integer' }] } })
    const { store } = renderWorkbench(designer(), mappingView({ spec }))
    const itemNames = () => store.getState().history.present.docSchema.root.children!.at(-1)!.items!.children!.map((f) => f.name)
    // AC1: the item fields show one level under the array, and collapse with it.
    expect(row('sku')).toHaveAttribute('aria-level', '2')
    expect(row('items')).toHaveAttribute('aria-expanded', 'true')
    await userEvent.click(screen.getByRole('button', { name: 'Collapse items' }))
    expect(row('sku')).toBeUndefined()
    await userEvent.click(screen.getByRole('button', { name: 'Expand items' }))

    // AC3: with the array selected, the button adds into its items and opens the name.
    expect(screen.getByRole('button', { name: 'Add field' })).toBeInTheDocument()
    await userEvent.click(row('items'))
    await userEvent.click(screen.getByRole('button', { name: 'Add field to items' }))
    await userEvent.type(await screen.findByRole('textbox', { name: 'Rename newField' }), '{Control>}a{/Control}price{Enter}')
    expect(itemNames()).toEqual(['sku', 'qty', 'price'])

    // With an item field selected, the next one goes right after it.
    await userEvent.click(row('sku'))
    await userEvent.click(screen.getByRole('button', { name: 'Add field to items' }))
    await userEvent.keyboard('{Escape}')
    expect(itemNames()).toEqual(['sku', 'newField', 'qty', 'price'])

    // AC4: the array's menu adds into its items too.
    await userEvent.click(screen.getByRole('button', { name: 'Actions for items' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: 'Add field' }))
    await userEvent.keyboard('{Escape}')
    expect(itemNames()).toHaveLength(5)
  })

  it('OC-145: click to rename', async () => {
    const { store } = renderWorkbench(designer())
    const names = () => store.getState().history.present.docSchema.root.children!.map((f) => f.name)
    const label = (n: string) => within(row(n)).getByText(n, { selector: 'span' })
    const renameBox = (n: string) => screen.queryByRole('textbox', { name: `Rename ${n}` })
    // AC1: the first click selects; a click on the selected field's name opens the rename box.
    await userEvent.click(label('email'))
    expect(renameBox('email')).toBeNull()
    expect(label('email')).toHaveAttribute('title', 'Click to rename (F2)')
    await userEvent.click(label('email'))
    await userEvent.type(renameBox('email')!, '{Control>}a{/Control}  mail  {Enter}')
    expect(names()).toEqual(['customerId', 'name', 'mail', 'createdAt', 'age', 'profile'])

    // AC3: Escape, a blank name or a sibling's name keep the old one.
    await userEvent.click(label('mail'))
    await userEvent.type(renameBox('mail')!, 'x{Escape}')
    await userEvent.click(label('mail'))
    await userEvent.type(renameBox('mail')!, '{Control>}a{/Control}{Backspace}   {Enter}')
    await userEvent.click(label('mail'))
    await userEvent.type(renameBox('mail')!, '{Control>}a{/Control}age{Enter}')
    expect(names()).toEqual(['customerId', 'name', 'mail', 'createdAt', 'age', 'profile'])

    // AC4: the rest of the row only selects; in a multi-selection a name click selects just that field.
    await userEvent.click(row('mail'))
    await userEvent.click(screen.getByRole('combobox', { name: 'Type of mail' }))
    expect(renameBox('mail')).toBeNull()
    await userEvent.click(label('age'))
    fireEvent.click(label('mail'), { shiftKey: true })
    expect(label('age')).not.toHaveAttribute('title')
    await userEvent.click(label('age'))
    expect(renameBox('age')).toBeNull()
    expect(row('age')).toHaveAttribute('aria-selected', 'true')
    expect(row('mail')).toHaveAttribute('aria-selected', 'false')
  })

  it('AC7: drop shows diff dialog and applies merge', async () => {
    const inferred: DocSchema = {
      source: 'json-sample',
      root: { id: 'r', name: '$', type: 'object', children: [{ id: 'n1', name: 'customerId', type: 'integer' }, { id: 'n2', name: 'nickname', type: 'string' }] },
    }
    const infer: unknown[] = []
    const design: unknown[] = []
    const merged = sampleSpec()
    merged.docSchema.root.children!.push({ id: 'n2', name: 'nickname', type: 'string' })
    server.use(
      http.post('*/api/v1/doc-schemas/infer', async ({ request }) => {
        infer.push(await request.json())
        return HttpResponse.json({ docSchema: inferred, warnings: [] })
      }),
      http.post('*/api/v1/projects/:pid/mappings/:mid/design', async ({ request }) => {
        design.push({ ifMatch: request.headers.get('If-Match'), body: await request.json() })
        return HttpResponse.json({ mapping: mappingView({ spec: merged, version: 4 }), orphans: [] })
      }),
    )
    const { store } = renderWorkbench(designer())

    const file = new File([JSON.stringify({ customerId: 1, nickname: 'Al' })], 'sample.json', { type: 'application/json' })
    fireEvent.drop(screen.getByRole('region', { name: 'Document design' }), { dataTransfer: { files: [file], types: ['Files'], getData: () => '' } })

    const dialog = await screen.findByRole('dialog', { name: 'Review design changes' })
    expect(infer).toEqual([{ kind: 'json-sample', content: '{"customerId":1,"nickname":"Al"}' }])
    expect(within(dialog).getByText('+ nickname')).toBeInTheDocument()
    expect(within(dialog).getByText('− name')).toBeInTheDocument()

    await userEvent.click(within(dialog).getByRole('button', { name: 'Merge' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(design).toEqual([{ ifMatch: '"3"', body: { docSchema: inferred, strategy: 'merge' } }])
    expect(store.getState().version).toBe(4)
    expect(names(store)).toContain('nickname')
  })

  it('AC7: merge uses server reconcile', async () => {
    // "Update, keep mappings" is the composer's re-import (OC-061): the server's reconcile.
    const inferred: DocSchema = { source: 'json-sample', root: { id: 'r', name: '$', type: 'object', children: [{ id: 'n1', name: 'customerId', type: 'integer' }] } }
    const design: unknown[] = []
    server.use(
      http.post('*/api/v1/doc-schemas/infer', () => HttpResponse.json({ docSchema: inferred, warnings: [] })),
      http.post('*/api/v1/projects/:pid/mappings/:mid/design', async ({ request }) => {
        design.push(await request.json())
        return HttpResponse.json({ mapping: mappingView({ version: 4 }), orphans: [], diff: { added: [], removed: [], typeChanged: [], renamed: [], moved: [] } })
      }),
    )
    renderWorkbench(designer())
    const file = new File(['{"customerId":1}'], 'sample.json', { type: 'application/json' })
    fireEvent.drop(screen.getByRole('region', { name: 'Document design' }), { dataTransfer: { files: [file], types: ['Files'], getData: () => '' } })
    const dialog = await screen.findByRole('dialog', { name: 'Review design changes' })
    await userEvent.click(within(dialog).getByRole('button', { name: 'Update, keep mappings' }))
    await waitFor(() => expect(design).toEqual([{ docSchema: inferred, strategy: 'reconcile' }]))
  })

  it('AC8: rename re-renders single row', async () => {
    const children: DocField[] = Array.from({ length: 1000 }, (_, i) => ({ id: `f${i}`, name: `field${i}`, type: 'string' }))
    const spec = { ...sampleSpec(), fieldMappings: [], docSchema: { source: 'manual' as const, root: { id: 'fld_root', name: '$', type: 'object' as const, children } } }
    const { store } = renderWorkbench(designer(), mappingView({ spec }))

    // Virtualized: a fraction of the 1,000 rows is in the DOM.
    const rendered = screen.getAllByRole('treeitem').length
    expect(rendered).toBeGreaterThan(10)
    expect(rendered).toBeLessThan(60)

    await userEvent.dblClick(row('field3'))
    const input = screen.getByRole('textbox', { name: 'Rename field3' })
    await userEvent.type(input, 'x')
    rowRenders.n = 0
    await userEvent.keyboard('{Enter}')

    expect(store.getState().history.present.docSchema.root.children![3]!.name).toBe('field3x')
    expect(rowRenders.n).toBe(1)
  })
})
