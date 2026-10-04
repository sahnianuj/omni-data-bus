import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { dragOnto } from '@/test/drag'
import { renderWorkbench } from '@/test/mapping'
import { DocumentTree } from './DocumentTree'

const noop = vi.fn()
const tree = () => <DocumentTree onBindTable={noop} onDropTable={noop} onDropColumn={noop} onMapColumn={noop} onPaste={noop} />
const node = (name: RegExp) => within(screen.getByRole('tree', { name: 'Document' })).getByRole('treeitem', { name })

describe('DocumentTree', () => {
  it('fields drag to a new place; the root takes them at the end', () => {
    const { store } = renderWorkbench(tree())
    const root = () => store.getState().history.present.docSchema.root
    // jsdom rows have no height: a drop counts as the row's middle (after a field, inside a container).
    dragOnto(node(/^customerId, integer/), node(/^email, string/))
    expect(root().children!.map((f) => f.name)).toEqual(['name', 'email', 'customerId', 'createdAt', 'age', 'profile'])
    dragOnto(node(/^age, integer/), node(/^name, object/))
    expect(root().children![0]!.children!.map((f) => f.name)).toEqual(['first', 'last', 'age'])
    dragOnto(node(/^first, string/), node(/^\$, object/))
    expect(root().children!.at(-1)!.name).toBe('first')
    expect(root().children![0]!.children!.map((f) => f.name)).toEqual(['last', 'age'])
  })

  it('the root and item rows are not draggable', () => {
    renderWorkbench(tree())
    expect(node(/^\$, object/)).not.toHaveAttribute('draggable', 'true')
    expect(node(/^email, string/)).toHaveAttribute('draggable', 'true')
  })

  it('OC-081 AC3: add field goes where the selection says', async () => {
    const { store } = renderWorkbench(tree())
    const root = () => store.getState().history.present.docSchema.root
    // Nothing chosen: the root is selected, so the field goes at the end.
    await userEvent.click(screen.getByRole('button', { name: 'Add field' }))
    await userEvent.keyboard('first{Enter}')
    expect(root().children!.at(-1)!.name).toBe('first')
    // An object: inside it, at the end.
    await userEvent.click(node(/^name, object/))
    await userEvent.click(screen.getByRole('button', { name: 'Add field to name' }))
    await userEvent.keyboard('middle{Enter}')
    expect(root().children![1]!.children!.map((f) => f.name)).toEqual(['first', 'last', 'middle'])
    // A plain field: right after it.
    await userEvent.click(node(/^customerId, integer/))
    await userEvent.click(screen.getByRole('button', { name: 'Add field' }))
    await userEvent.keyboard('{Escape}')
    expect(root().children!.slice(0, 3).map((f) => f.name)).toEqual(['customerId', 'field', 'name'])
  })
})
