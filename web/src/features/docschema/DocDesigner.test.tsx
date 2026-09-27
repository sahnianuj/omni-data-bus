import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { DocField, DocSchema } from '@/features/mapping/types'
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
