import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { MappingIssue, MappingSpec } from '@/features/mapping/types'
import { mockComposer } from '@/test/composer'
import { mappingView, mockMapping, sampleSpec } from '@/test/mapping'
import { server, setupMswServer } from '@/test/msw'
import { makeProject, mockProject, repo } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'

setupMswServer()
beforeEach(() => {
  window.localStorage.clear()
  window.matchMedia = ((q: string) => ({ matches: false, media: q, addEventListener: () => {}, removeEventListener: () => {} })) as never
  mockSession('owner')
  mockProject(makeProject({ source: repo('postgres'), target: repo('couchbase'), mappings: 1, readyMappings: 0 }))
  mockComposer()
  server.use(http.get('*/api/v1/projects/:pid/mappings', () => HttpResponse.json({ items: [] })))
})

describe('Combine rows with the same key (OC-066)', () => {
  it('OC-066 AC2: combine from canvas', async () => {
    // The flat detail mapping (order_items: one row per line) keyed on order_id only.
    const spec = sampleSpec()
    spec.docSchema.root.children = [
      { id: 'f_oid', name: 'orderId', type: 'integer' },
      { id: 'f_line', name: 'lineNo', type: 'integer' },
      { id: 'f_qty', name: 'quantity', type: 'integer' },
    ]
    spec.fieldMappings = [
      { id: 'm_oid', targetFieldId: 'f_oid', sources: [{ column: 'order_id' }], transforms: [], origin: 'manual', state: 'active' },
      { id: 'm_line', targetFieldId: 'f_line', sources: [{ column: 'line_no' }], transforms: [], origin: 'manual', state: 'active' },
      { id: 'm_qty', targetFieldId: 'f_qty', sources: [{ column: 'quantity' }], transforms: [], origin: 'manual', state: 'active' },
    ]
    spec.key = { template: 'order::{order_id}' }
    const issue: MappingIssue = {
      severity: 'warning', code: 'KEY_MAY_COLLIDE', path: 'key',
      message: "The key's columns don't cover the primary key or a unique constraint, so documents may overwrite each other. Rows with the same order_id can be combined into one document instead.",
      fix: { kind: 'combine-rows', label: 'Combine rows with the same key', columns: ['order_id'] },
    }
    const state = mockMapping(mappingView({ sourceSchema: 'public', sourceTable: 'order_items', spec, embedTables: {}, embedCandidates: [], issues: [issue] }))
    renderRoutes(routes, '/projects/p1/mappings/m1')
    await screen.findByRole('tab', { name: 'Canvas', selected: true })
    await userEvent.click(screen.getByRole('tab', { name: /^Issues/ }))
    await userEvent.click(await screen.findByRole('button', { name: 'Combine rows with the same key' }))

    const dialog = await screen.findByRole('dialog', { name: 'Combine rows with the same key' })
    expect(within(dialog).getByRole('textbox', { name: 'New array name' })).toHaveValue('orderItems')
    expect(within(dialog).getByRole('checkbox', { name: 'lineNo' })).toBeChecked()
    expect(within(dialog).getByRole('checkbox', { name: 'quantity' })).toBeChecked()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Combine' }))

    // Composed, opened in the Composer: one document per order_id, its lines in orderItems[].
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Composer' })).toHaveAttribute('aria-selected', 'true'))
    expect(await screen.findByRole('treeitem', { name: /^orderItems, array, bound to order_items/ })).toBeInTheDocument()
    await waitFor(() => expect((state.puts.at(-1)?.body as { spec: MappingSpec } | undefined)?.spec.root?.groupBy).toEqual(['order_id']), { timeout: 5000 })
    const saved = (state.puts.at(-1)!.body as { spec: MappingSpec }).spec
    expect(saved.mode).toBe('composed')
    const group = saved.relations.find((r) => r.group)!
    expect(saved.fieldMappings.find((m) => m.id === 'm_line')!.sources).toEqual([{ column: 'line_no', relation: group.id }])
    expect(group.orderBy).toEqual([{ column: 'line_no' }])
  })
})
