import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { useState } from 'react'
import { beforeEach, describe, expect, it } from 'vitest'
import type { ReconcileResult } from '@/features/mapping/api'
import type { DocSchema } from '@/features/mapping/types'
import { composedSpec, composedView, mockComposer } from '@/test/composer'
import { renderWorkbench } from '@/test/mapping'
import { server, setupMswServer } from '@/test/msw'
import { mockSession } from '@/test/session'
import { adoptDesign } from './composerOps'
import { ReimportStep } from './ReimportDialog'

setupMswServer()

/** The customer's revision of the composer fixture: orderId → orderNo, items[].sku gone, currency added. */
const incoming: DocSchema = { source: 'json-sample', root: { id: 'fld_root', name: '$', type: 'object', children: [] } }

/** What the server's reconcile returns, by the declined ids it's sent. */
function reconciled(declined: string[]): ReconcileResult {
  const s = composedSpec().docSchema
  const kids = s.root.children!.map((c) => (c.id === 'f_oid' ? { ...c, name: declined.includes('f_oid') ? 'x' : 'orderNo' } : c))
  const items = kids.find((c) => c.id === 'f_items')!
  items.items = { ...items.items!, children: items.items!.children!.filter((c) => c.id !== 'f_sku') }
  const removed = [{ fieldId: 'f_sku', path: 'items[].sku', type: 'string' as const }]
  if (declined.includes('f_oid')) {
    kids.splice(0, 1, { id: 'n_ono', name: 'orderNo', type: 'integer' })
    removed.unshift({ fieldId: 'f_oid', path: 'orderId', type: 'string' as never })
  }
  kids.push({ id: 'n_cur', name: 'currency', type: 'string' })
  return {
    docSchema: { ...s, root: { ...s.root, children: kids } },
    diff: {
      added: [{ fieldId: 'n_cur', path: 'currency', type: 'string' }, ...(declined.includes('f_oid') ? [{ fieldId: 'n_ono', path: 'orderNo', type: 'integer' as const }] : [])],
      removed,
      typeChanged: [],
      renamed: declined.includes('f_oid') ? [] : [{ fieldId: 'f_oid', from: 'orderId', to: 'orderNo' }],
      moved: [],
    },
  }
}

let bodies: { declined?: string[] }[] = []
beforeEach(() => {
  mockSession('owner')
  mockComposer()
  bodies = []
  server.use(
    http.post('*/api/v1/doc-schemas/reconcile', async ({ request }) => {
      const body = (await request.json()) as { declined?: string[] }
      bodies.push(body)
      return HttpResponse.json(reconciled(body.declined ?? []))
    }),
  )
})

function renderStep() {
  let done = 0
  function Harness() {
    const [open, setOpen] = useState(true)
    return open ? <ReimportStep incoming={incoming} onBack={() => {}} onDone={() => (done++, setOpen(false))} /> : null
  }
  const res = renderWorkbench(<Harness />, composedView())
  return { ...res, done: () => done }
}

describe('ReimportDialog', () => {
  it('AC2: decline rename', async () => {
    const { store } = renderStep()
    const rename = await screen.findByRole('checkbox', { name: 'orderId renamed to orderNo' })
    expect(rename).toBeChecked()
    await userEvent.click(rename)
    await waitFor(() => expect(bodies.at(-1)).toMatchObject({ declined: ['f_oid'] }))
    const declined = await screen.findByRole('checkbox', { name: 'orderId: removed and added as a new field' })
    expect(declined).not.toBeChecked()
    expect(screen.getByRole('list', { name: '2 fields added (unmapped)' })).toHaveTextContent('+ orderNo (integer)')
    // Re-accepting sends no declined ids again.
    await userEvent.click(declined)
    await waitFor(() => expect(bodies.at(-1)).not.toHaveProperty('declined'))
    await screen.findByRole('checkbox', { name: 'orderId renamed to orderNo' })
    expect(store.getState().history.present).toEqual(composedSpec()) // nothing applied yet
  })

  it('AC3: confirm removals', async () => {
    const { store, done } = renderStep()
    const removed = await screen.findByRole('list', { name: '1 field removed' })
    expect(removed).toHaveTextContent('− items[].sku')
    // items[].sku is mapped: nothing is removed until confirmed.
    const submit = screen.getByRole('button', { name: 'Update design' })
    expect(submit).toBeDisabled()
    await userEvent.click(screen.getByRole('checkbox', { name: /^Remove 1 fields with their 1 mappings and 0 bindings$/ }))
    await userEvent.click(submit)
    expect(done()).toBe(1)
    const spec = store.getState().history.present
    expect(spec.docSchema.root.children!.find((c) => c.id === 'f_oid')!.name).toBe('orderNo')
    expect(spec.fieldMappings.some((m) => m.targetFieldId === 'f_sku')).toBe(false)
    expect(spec.fieldMappings.find((m) => m.targetFieldId === 'f_oid')).toBeDefined() // kept through the rename
    expect(spec.relations).toEqual(composedSpec().relations)
    store.getState().undo()
    expect(store.getState().history.present).toEqual(composedSpec())
  })
})

describe('adoptDesign', () => {
  it('drops what pointed at removed fields, with nested relations', () => {
    const s = composedSpec()
    const without = { ...s.docSchema, root: { ...s.docSchema.root, children: s.docSchema.root.children!.filter((c) => c.id !== 'f_items' && c.id !== 'f_count') } }
    const out = adoptDesign(s, without)
    expect(out.relations.map((r) => r.id)).toEqual(['rel_cust', 'rel_tags']) // items and what nests under it
    expect(out.aggregates).toEqual([])
    expect(out.fieldMappings.map((m) => m.id)).toEqual(['m_oid', 'm_cname', 'm_label'])
  })
})
