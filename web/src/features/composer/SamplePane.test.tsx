import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import type { MappingSpec } from '@/features/mapping/types'
import { composedSpec, composedView, mockComposer } from '@/test/composer'
import { renderWorkbench } from '@/test/mapping'
import { server, setupMswServer } from '@/test/msw'
import { mockSession } from '@/test/session'
import { SamplePane } from './SamplePane'

setupMswServer()
beforeEach(() => {
  mockSession('owner')
  mockComposer()
})

function importedSpec(): MappingSpec {
  const s = composedSpec()
  return { ...s, docSchema: { ...s.docSchema, reference: { orderId: 1, currency: 'EUR' }, imported: { at: '2026-09-30T00:00:00Z', format: 'sample', samples: 1, fieldCount: 2, fields: [{ path: 'orderId', type: 'integer' }, { path: 'currency', type: 'string' }] } } }
}

describe('SamplePane', () => {
  it('OC-061 AC5: shows the document’s conformance beside the reference', async () => {
    server.use(
      http.post('*/api/v1/projects/:pid/mappings/:mid/preview', () =>
        HttpResponse.json({
          items: [{ sourceRow: {}, key: 'order::1', document: { orderId: '1' }, sizeBytes: 14, warnings: [], error: null, conformance: { missing: ['currency'], extra: [], typeDiff: ['orderId'] } }],
          summary: { ok: 1, errors: 0, avgSizeBytes: 14, maxSizeBytes: 14 },
          writeMode: 'upsert',
        }),
      ),
    )
    const restore = { severity: 'info' as const, code: 'REFERENCE_FIELD_UNMAPPED', path: 'currency', nodePath: 'currency', message: '', fix: { kind: 'restore-field' as const, label: 'Add currency back (from currency)', columns: ['currency'] } }
    const { store } = renderWorkbench(<SamplePane />, composedView({ spec: { ...importedSpec(), referenceStrict: true }, issues: [restore] }))
    const ref = await screen.findByRole('region', { name: 'Reference' })
    const diff = await within(ref).findByRole('list', { name: 'Differs from the imported design' })
    expect(ref).toHaveTextContent("This sample differs from the customer's document:")
    expect(diff).toHaveTextContent("currency is in the customer's document but not generated.")
    expect(diff).toHaveTextContent("orderId has another type than in the customer's document.")
    expect(ref).toHaveTextContent('"EUR"')

    // OC-071 AC3: each difference has its fix.
    await userEvent.click(within(diff).getByRole('button', { name: 'Add currency back (from currency)' }))
    const spec = store.getState().history.present
    const added = spec.docSchema.root.children!.find((c) => c.name === 'currency')!
    expect(added.type).toBe('string')
    expect(spec.fieldMappings.find((m) => m.targetFieldId === added.id)?.sources).toEqual([{ column: 'currency' }])
  })

  it('OC-076 AC1: lists no differences unless strict', async () => {
    server.use(
      http.post('*/api/v1/projects/:pid/mappings/:mid/preview', () =>
        HttpResponse.json({
          items: [{ sourceRow: {}, key: 'order::1', document: { orderId: '1' }, sizeBytes: 14, warnings: [], error: null, conformance: { missing: ['currency'], extra: [], typeDiff: ['orderId'] } }],
          summary: { ok: 1, errors: 0, avgSizeBytes: 14, maxSizeBytes: 14 },
          writeMode: 'upsert',
        }),
      ),
    )
    renderWorkbench(<SamplePane />, composedView({ spec: importedSpec() }))
    const ref = await screen.findByRole('region', { name: 'Reference' })
    expect(await within(ref).findByText(/"EUR"/)).toBeInTheDocument()
    expect(ref).not.toHaveTextContent("differs from the customer's document")
    expect(within(ref).queryByRole('list', { name: 'Differs from the imported design' })).not.toBeInTheDocument()
  })
})
