import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { withFixedLayout } from '@/test/layout'
import { server, setupMswServer } from '@/test/msw'
import { embedView, mappingView, renderWorkbench, scol } from '@/test/mapping'
import { mockSession } from '@/test/session'
import type { AddEmbedRequest } from './api'
import { MappingCanvas } from './MappingCanvas'

setupMswServer()
withFixedLayout(320, 1200)
beforeEach(() => mockSession('owner'))

/** The n-th Embed button, once the session enabled it (editor-only). */
async function embedButton(n: number) {
  await waitFor(() => expect(screen.getAllByRole('button', { name: 'Embed' })[n]).toBeEnabled())
  return screen.getAllByRole('button', { name: 'Embed' })[n]!
}

describe('EmbedDialog', () => {
  it('AC2: creates embed and field', async () => {
    const bodies: { ifMatch: string | null; body: AddEmbedRequest }[] = []
    server.use(
      http.get('*/api/v1/connections/:id/schema/:schema/:table', ({ params }) => {
        expect(params.id).toBe('p1::source')
        return HttpResponse.json({ name: 'page_view', columns: [scol('view_id', 'integer'), scol('customer_id', 'integer'), scol('viewed_at', 'datetime')] })
      }),
      http.post('*/api/v1/projects/:pid/mappings/:mid/embeds', async ({ request }) => {
        const body = (await request.json()) as AddEmbedRequest
        bodies.push({ ifMatch: request.headers.get('If-Match'), body })
        // The server adds the field, the embed and the auto-mapped items.
        const next = embedView()
        next.spec.docSchema.root.children!.push({ id: 'f_views', name: 'views', type: 'array', items: { id: 'f_vi', name: '[]', type: 'object', children: [{ id: 'f_at', name: 'viewedAt', type: 'datetime' }] } })
        next.spec.embeds.push({ id: 'emb_pv', kind: 'one-to-many', table: { schema: 'shop', name: 'page_view' }, join: [{ parent: 'customer_id', child: 'customer_id' }], targetFieldId: 'f_views', limit: 50 })
        next.spec.fieldMappings.push({ id: 'm_at', targetFieldId: 'f_at', sources: [{ column: 'viewed_at', embed: 'emb_pv' }], transforms: [], origin: 'auto', state: 'active' })
        next.embedTables!.emb_pv = { table: { schema: 'shop', name: 'page_view' }, columns: [scol('view_id', 'integer'), scol('customer_id', 'integer'), scol('viewed_at', 'datetime')] }
        return HttpResponse.json({ ...next, version: 4 })
      }),
    )
    const { store } = renderWorkbench(<MappingCanvas />, embedView())
    await userEvent.click(await embedButton(1))
    const dialog = await screen.findByRole('dialog', { name: 'Embed page_view' })
    expect(dialog).toHaveTextContent('Array of objects (one-to-many)')
    const name = screen.getByLabelText('Field name')
    expect(name).toHaveValue('pageView')
    await userEvent.clear(name)
    await userEvent.type(name, 'views')
    await waitFor(() => expect(screen.getByRole('option', { name: 'viewed_at' })).toBeInTheDocument())
    await userEvent.selectOptions(screen.getByLabelText('Order items by'), 'viewed_at')
    await userEvent.click(screen.getByLabelText('Descending'))
    await userEvent.clear(screen.getByLabelText('At most'))
    await userEvent.type(screen.getByLabelText('At most'), '50')
    expect(screen.getByLabelText(/Generate fields from table/)).toBeChecked()
    await userEvent.click(screen.getByRole('button', { name: 'Embed' }))

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(bodies).toEqual([{
      ifMatch: '"3"',
      body: {
        kind: 'one-to-many', table: { schema: 'shop', name: 'page_view' }, join: [{ parent: 'customer_id', child: 'customer_id' }], targetName: 'views',
        generate: true, limit: 50, empty: 'array', orderBy: [{ column: 'viewed_at', desc: true }],
      },
    }])
    // The new field and the embedded group appear.
    expect(store.getState().version).toBe(4)
    expect(store.getState().history.present.embeds.map((e) => e.id)).toEqual(['emb_o', 'emb_pv'])
    expect(screen.getByTestId('embed-group-emb_pv')).toHaveTextContent('page_view → views[] (array)')
    expect(screen.getByTestId('source-emb_pv:viewed_at')).toBeInTheDocument()
    expect(screen.getByTestId('target-views[].viewedAt')).toBeInTheDocument()
  })

  it('many-to-one has no array options; errors stay in the dialog', async () => {
    server.use(
      http.get('*/api/v1/connections/:id/schema/:schema/:table', () => HttpResponse.json({ name: 'address', columns: [] })),
      http.post('*/api/v1/projects/:pid/mappings/:mid/embeds', () =>
        HttpResponse.json({ type: 'about:blank', title: "Can't embed", status: 422, code: 'VALIDATION_FAILED', detail: 'Column address_id isn\'t in customer.' }, { status: 422 }),
      ),
    )
    renderWorkbench(<MappingCanvas />, embedView({ ...mappingView(), embedCandidates: embedView().embedCandidates, embedTables: {} }))
    await userEvent.click(await embedButton(0))
    await screen.findByRole('dialog', { name: 'Embed address' })
    expect(screen.queryByLabelText('At most')).not.toBeInTheDocument()
    expect(screen.getByText('Object (many-to-one)')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Embed' }))
    expect(await screen.findByText("Column address_id isn't in customer.")).toBeInTheDocument()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })
})
