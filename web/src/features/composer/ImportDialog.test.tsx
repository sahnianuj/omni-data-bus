import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { useState } from 'react'
import { beforeEach, describe, expect, it } from 'vitest'
import type { DocSchema } from '@/features/mapping/types'
import { composedView, mockComposer } from '@/test/composer'
import { renderWorkbench } from '@/test/mapping'
import { server, setupMswServer } from '@/test/msw'
import { mockSession } from '@/test/session'
import { DocumentTree } from './DocumentTree'
import { ImportDialog } from './ImportDialog'

setupMswServer()

/** What the server infers from ORDERS (docschema.Import; see import_test.go). */
const inferredOrders: DocSchema = {
  source: 'json-sample',
  reference: { orderId: 1001, orderDate: '2024-03-01T10:00:00Z', items: [{ sku: 'A' }] },
  imported: { at: '2026-09-30T00:00:00Z', fileName: 'orders.ndjson', format: 'ndjson', samples: 3, fieldCount: 6 },
  constantHints: [{ fieldId: 'i_type', value: 'order', reason: 'discriminator-name' }],
  root: {
    id: 'fld_root', name: '$', type: 'object',
    children: [
      { id: 'i_oid', name: 'orderId', type: 'integer', required: true, example: 1001 },
      { id: 'i_type', name: 'type', type: 'string', required: true, example: 'order' },
      { id: 'i_date', name: 'orderDate', type: 'datetime', format: 'iso8601', required: true, example: '2024-03-01T10:00:00Z' },
      { id: 'i_items', name: 'items', type: 'array', required: true, items: { id: 'i_item', name: '[]', type: 'object', children: [{ id: 'i_sku', name: 'sku', type: 'string', required: true, example: 'A' }] } },
      { id: 'i_coupon', name: 'couponCode', type: 'string', example: 'SPRING' },
    ],
  },
}

const ORDERS = ['{"orderId":1001,"type":"order","orderDate":"2024-03-01T10:00:00Z","items":[{"sku":"A"}]}', '{"orderId":1002,"type":"order","orderDate":"2024-03-02T10:00:00Z","couponCode":"SPRING","items":[]}', '{"orderId":1003,"type":"order","orderDate":"2024-03-03T10:00:00Z","items":[]}'].join('\n')

let requests: unknown[] = []
beforeEach(() => {
  mockSession('owner')
  mockComposer()
  requests = []
  server.use(
    http.post('*/api/v1/doc-schemas/infer', async ({ request }) => {
      const body = (await request.json()) as { kind: string }
      requests.push(body)
      return HttpResponse.json({ docSchema: body.kind === 'json-schema' ? schemaResult : inferredOrders, warnings: body.kind === 'json-schema' ? ['Ignored unsupported keyword "pattern" at $.sku.'] : [] })
    }),
  )
})

const schemaResult: DocSchema = {
  source: 'json-schema',
  imported: { at: '2026-09-30T00:00:00Z', format: 'json-schema', samples: 0, fieldCount: 2 },
  root: { id: 'fld_root', name: '$', type: 'object', children: [{ id: 's_id', name: 'id', type: 'integer', required: true, description: 'Order id', example: 7 }, { id: 's_at', name: 'placed', type: 'datetime', format: 'iso8601' }] },
}

function renderImport(nodeId: string | null = null) {
  let closed = false
  function Harness() {
    const [open, setOpen] = useState(true)
    return (
      <>
        <DocumentTree onBindTable={() => {}} onDropTable={() => {}} onDropColumn={() => {}} onMapColumn={() => {}} onPaste={() => {}} />
        {open ? <ImportDialog nodeId={nodeId} onClose={() => ((closed = true), setOpen(false))} /> : null}
      </>
    )
  }
  const res = renderWorkbench(<Harness />, composedView())
  return { ...res, closed: () => closed }
}

const tree = () => screen.getByRole('tree', { name: 'Document' })

describe('ImportDialog', () => {
  it('AC1: drop ndjson file', async () => {
    const { store, closed } = renderImport()
    const dialog = screen.getByRole('dialog', { name: 'Import a JSON design' })
    const file = new File([ORDERS], 'orders.ndjson', { type: 'application/x-ndjson' })
    fireEvent.drop(within(dialog).getByTestId('import-drop'), { dataTransfer: { files: [file], types: ['Files'] } })
    expect(await within(dialog).findByText('orders.ndjson')).toBeInTheDocument()
    expect(within(dialog).getByText('3 samples')).toBeInTheDocument()

    await userEvent.click(within(dialog).getByRole('button', { name: 'Read design' }))
    const report = await within(dialog).findByRole('status', { name: 'What will be imported' })
    expect(report).toHaveTextContent('6 fields · NDJSON · 3 samples')
    expect(report).toHaveTextContent('1 optional field (missing from some samples)')
    expect(report).toHaveTextContent('1 value looks fixed')
    expect(report).toHaveTextContent('The first sample is kept as the reference document.')
    expect(report).toHaveTextContent(/Replaces 5 bindings and \d+ mappings/)
    expect(requests).toEqual([{ kind: 'json-sample', content: ORDERS, fileName: 'orders.ndjson' }])

    await userEvent.click(within(dialog).getByRole('button', { name: 'Replace design' }))
    expect(closed()).toBe(true)
    const spec = store.getState().history.present
    // The server's design, verbatim: order, types, examples, reference and hints.
    expect(spec.docSchema).toEqual(inferredOrders)
    expect(spec.relations).toEqual([])
    expect(spec.fieldMappings).toEqual([])
    expect(within(tree()).getAllByRole('treeitem').map((n) => n.getAttribute('aria-label'))).toEqual([
      '$, object', 'orderId, integer', 'type, string', 'orderDate, datetime', 'items, array', '{ } each item, object', 'sku, string', 'couponCode, string',
    ])
    // Examples show beside unmapped fields.
    expect(within(tree()).getByRole('treeitem', { name: 'orderDate, datetime' })).toHaveTextContent('"2024-03-01T10:00:00Z"')
  })

  it('AC2: JSON Schema sniffed and imported', async () => {
    const { store } = renderImport()
    const dialog = screen.getByRole('dialog')
    const schema = '{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"id":{"type":"integer"}}}'
    await userEvent.click(within(dialog).getByRole('textbox', { name: 'JSON design' }))
    await userEvent.paste(schema)
    expect(within(dialog).getByText('JSON Schema')).toBeInTheDocument()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Read design' }))
    const report = await within(dialog).findByRole('status', { name: 'What will be imported' })
    expect(report).toHaveTextContent('2 fields · JSON Schema')
    expect(report).toHaveTextContent('Ignored unsupported keyword "pattern" at $.sku.')
    expect(requests).toEqual([{ kind: 'json-schema', content: schema }])
    await userEvent.click(within(dialog).getByRole('button', { name: 'Replace design' }))
    const id = store.getState().history.present.docSchema.root.children![0]!
    expect(id).toEqual({ id: 's_id', name: 'id', type: 'integer', required: true, description: 'Order id', example: 7 })
  })

  it('AC6: same design as Design tab', async () => {
    // The composer sends exactly what the Design tab sends (DocDesigner.test.tsx: {kind, content})
    // and applies the server's design unchanged: there's no inference in the browser any more.
    const { store } = renderImport()
    const dialog = screen.getByRole('dialog')
    await userEvent.click(within(dialog).getByRole('textbox', { name: 'JSON design' }))
    await userEvent.paste('{"orderId":1}')
    expect(within(dialog).getByText('1 sample')).toBeInTheDocument()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Read design' }))
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Replace design' }))
    expect(requests).toEqual([{ kind: 'json-sample', content: '{"orderId":1}' }])
    expect(store.getState().history.present.docSchema).toEqual(inferredOrders)
    expect(await import('./composerOps')).not.toHaveProperty('inferField')
  })

  it('imports onto a node: reports the names kept, and chooses a file with the button', async () => {
    server.use(http.post('*/api/v1/doc-schemas/infer', () => HttpResponse.json({ docSchema: { source: 'json-sample', root: { id: 'fld_root', name: '$', type: 'object', children: [{ id: 'n_sku', name: 'sku', type: 'string' }, { id: 'n_disc', name: 'discount', type: 'number' }] } }, warnings: [] })))
    const { store } = renderImport('f_items')
    const dialog = screen.getByRole('dialog', { name: 'Import JSON into items' })
    await userEvent.upload(within(dialog).getByLabelText('JSON or NDJSON file'), new File(['{"sku":"B","discount":0.1}'], 'line.json', { type: 'application/json' }))
    expect(await within(dialog).findByText('line.json')).toBeInTheDocument()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Read design' }))
    const report = await within(dialog).findByRole('status')
    expect(report).toHaveTextContent('1 field')
    expect(report).toHaveTextContent('Already here, kept as they are: sku')
    expect(report).not.toHaveTextContent('Replaces')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Add fields' }))
    await waitFor(() => expect(within(tree()).getByRole('treeitem', { name: /^discount, number/ })).toBeInTheDocument())
    expect(store.getState().history.present.relations).toHaveLength(5)
  })
})
