import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Proposal } from '@/features/mapping/api'
import { cartDesign, mockPOS, openCart } from '@/test/designFirst'
import { server, setupMswServer } from '@/test/msw'
import { makeProject, mockProject, repo } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'
import paymentsJSON from '../../../testdata/design-first/cart-payments-proposal.json'

setupMswServer()
const withPayments = paymentsJSON as unknown as Proposal
beforeEach(() => {
  window.localStorage.clear()
  mockSession('owner')
  mockProject(makeProject({ source: repo('postgres'), target: repo('couchbase'), mappings: 1, readyMappings: 0 }))
  server.use(http.get('*/api/v1/projects/:pid/mappings', () => HttpResponse.json({ items: [] })))
})

const tree = () => screen.getByRole('tree', { name: 'Document' })

describe('Variant shapes (OC-171)', () => {
  it('OC-171 AC1: several files are one import; the first is the document', async () => {
    mockPOS()
    openCart()
    let body: { content?: string; fileName?: string; files?: { name: string }[] } = {}
    server.use(
      http.post('*/api/v1/doc-schemas/infer', async ({ request }) => {
        body = (await request.json()) as typeof body
        return HttpResponse.json({ docSchema: cartDesign, warnings: [] })
      }),
    )
    renderRoutes(routes, '/projects/p1/mappings/m1')
    await userEvent.click(await screen.findByRole('button', { name: 'Import JSON design…' }, { timeout: 8000 }))
    const dialog = await screen.findByRole('dialog')
    const schema = (props: string) => `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{${props}}}`
    const file = (name: string, text: string, rel: string) => {
      const f = new File([text], name, { type: 'application/json' })
      Object.defineProperty(f, 'webkitRelativePath', { value: rel })
      return f
    }
    fireEvent.change(within(dialog).getByLabelText('A folder of design files'), {
      target: { files: [file('scc.schema.json', schema('"paymentType":{"const":"SCC"}'), 'designs/payments/scc.schema.json'), file('cart.schema.json', schema('"id":{"type":"string"}'), 'designs/cart.schema.json')] },
    })
    expect(await within(dialog).findByTestId('import-extra')).toHaveTextContent('with 1 more file: payments/scc.schema.json')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Read design' }))
    await waitFor(() => expect(body.fileName).toBe('cart.schema.json'))
    expect(body.files?.map((f) => f.name)).toEqual(['payments/scc.schema.json'])
  }, 20_000)

  it("OC-171 AC5: a variant's rows are set in the Inspector; one undo restores them", async () => {
    mockPOS({ proposal: withPayments })
    openCart({ spec: { ...withPayments.spec } })
    renderRoutes(routes, '/projects/p1/mappings/m1')
    const payments = await within(await screen.findByRole('tree', { name: 'Document' }, { timeout: 8000 })).findByRole('treeitem', { name: /^payments, array, bound to collected_payments/ })
    await userEvent.click(payments)
    const rules = await screen.findByRole('group', { name: 'Variants' })
    const scc = within(rules).getByRole('combobox', { name: 'SCC: Column' })
    expect(scc).toHaveValue('scc_id')
    expect(within(tree()).getAllByRole('treeitem', { name: /^SCC, variant/ }).length).toBeGreaterThan(0)
    await userEvent.selectOptions(scc, 'svc_id')
    await waitFor(() => expect(within(rules).getByRole('combobox', { name: 'SCC: Column' })).toHaveValue('svc_id'))
    await userEvent.click(screen.getByRole('button', { name: 'Undo' }))
    await waitFor(() => expect(within(screen.getByRole('group', { name: 'Variants' })).getByRole('combobox', { name: 'SCC: Column' })).toHaveValue('scc_id'))
    await userEvent.selectOptions(within(screen.getByRole('group', { name: 'Variants' })).getByRole('combobox', { name: 'A row of no variant' }), 'fail')
    expect(within(screen.getByRole('group', { name: 'Variants' })).getByRole('combobox', { name: 'A row of no variant' })).toHaveValue('fail')
  }, 20_000)
})
