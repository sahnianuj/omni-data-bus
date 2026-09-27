import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { setupMswServer } from '@/test/msw'
import { mockPreview, renderWorkbench } from '@/test/mapping'
import { PreviewDrawer } from './PreviewDrawer'
import type { MappingPreview } from './types'

setupMswServer()

const preview: MappingPreview = {
  writeMode: 'upsert',
  items: [
    {
      key: 'customer::1',
      document: { customerId: 1, name: { first: 'ANN' }, type: 'customer' },
      sourceRow: { customer_id: 1, first_name: ' ann ' },
      sizeBytes: 64,
      warnings: [],
      error: null,
      trace: { 'name.first': [{ step: 'source', value: ' ann ' }, { step: 'trim', value: 'ann' }, { step: 'upper', value: 'ANN' }] },
      keyExists: true,
    },
    {
      key: 'customer::2',
      sourceRow: { customer_id: 2, first_name: 'x' },
      sizeBytes: 0,
      warnings: [],
      error: { code: 'TRANSFORM_FAILED', fieldPath: 'name.first', op: 'parseInteger', message: "can't parse 'x' as an integer" },
    },
  ],
  summary: { ok: 1, errors: 1, avgSizeBytes: 64, maxSizeBytes: 64, collisions: 1 },
}

describe('PreviewDrawer', () => {
  it('AC5: shows error and jump-to-mapping', async () => {
    const bodies = mockPreview(preview)
    const { store } = renderWorkbench(<PreviewDrawer projectId="p1" open onOpenChange={() => {}} />)
    expect(await screen.findByText('✓ 1 valid')).toBeInTheDocument()
    expect(screen.getByText('✗ 1 error')).toBeInTheDocument()
    expect(bodies[0]).toMatchObject({ limit: 10, sampleMode: 'first', specOverride: expect.any(Object) })
    expect(screen.getByText(/will be overwritten \(upsert\)/)).toBeInTheDocument()

    const rows = screen.getByRole('list', { name: 'Sample rows' })
    await userEvent.click(within(rows).getByText('customer::2'))
    expect(screen.getByRole('alert')).toHaveTextContent("can't parse 'x' as an integer")
    await userEvent.click(screen.getByRole('button', { name: 'Jump to mapping' }))
    expect(store.getState().selection).toEqual({ kind: 'mapping', id: 'm_first' })
  })

  it('"Include unmapped fields" is on by default; clearing it updates the spec and re-runs the preview', async () => {
    const bodies = mockPreview(preview)
    const { store } = renderWorkbench(<PreviewDrawer projectId="p1" open onOpenChange={() => {}} />)
    await screen.findByText('✓ 1 valid')
    const box = screen.getByRole('checkbox', { name: 'Include unmapped fields' })
    expect(box).toBeChecked()
    expect(bodies[0]!.specOverride?.omitUnmapped).toBeFalsy()
    await userEvent.click(box)
    expect(box).not.toBeChecked()
    expect(store.getState().history.present.omitUnmapped).toBe(true)
    await waitFor(() => expect(bodies.at(-1)!.specOverride?.omitUnmapped).toBe(true))
  })

  it('AC6: renders step trace', async () => {
    mockPreview(preview)
    renderWorkbench(<PreviewDrawer projectId="p1" open onOpenChange={() => {}} />)
    await screen.findByText('✓ 1 valid')
    await userEvent.click(screen.getByRole('tab', { name: 'Trace' }))
    const trace = screen.getByRole('list', { name: 'Trace' })
    expect(within(trace).getByText('name.first')).toBeInTheDocument()
    expect(within(trace).getByText('" ann "')).toBeInTheDocument()
    expect(within(trace).getByText('"ann"')).toBeInTheDocument()
    expect(within(trace).getByText('"ANN"')).toBeInTheDocument()
  })

  it('refresh re-requests with the chosen sample options', async () => {
    const bodies = mockPreview(preview)
    renderWorkbench(<PreviewDrawer projectId="p1" open onOpenChange={() => {}} />)
    await screen.findByText('✓ 1 valid')
    await userEvent.selectOptions(screen.getByLabelText('Sample'), '25')
    await userEvent.selectOptions(screen.getByLabelText('Sampling'), 'random')
    await userEvent.click(screen.getByLabelText('Check key collisions'))
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(bodies.at(-1)).toMatchObject({ limit: 25, sampleMode: 'random', checkKeys: true })
  })

  it('reopening after edits previews the current key and fields', async () => {
    const bodies = mockPreview(preview)
    let setOpen: (o: boolean) => void = () => {}
    function Harness() {
      const [open, set] = useState(true)
      setOpen = set
      return <PreviewDrawer projectId="p1" open={open} onOpenChange={set} />
    }
    const { store } = renderWorkbench(<Harness />)
    await waitFor(() => expect(bodies).toHaveLength(1))
    expect(bodies[0]!.specOverride!.key.template).toBe('customer::{customer_id}')

    act(() => setOpen(false))
    act(() =>
      store.getState().apply((s) => ({
        ...s,
        key: { template: 'cust::{customer_id}::{email_address}' },
        docSchema: { ...s.docSchema, root: { ...s.docSchema.root, children: [...s.docSchema.root.children!, { id: 'f_new', name: 'loyalty', type: 'string' }] } },
      })),
    )
    act(() => setOpen(true))
    await waitFor(() => expect(bodies).toHaveLength(2))
    expect(bodies[1]!.specOverride!.key.template).toBe('cust::{customer_id}::{email_address}')
    expect(bodies[1]!.specOverride!.docSchema.root.children!.map((f) => f.name)).toContain('loyalty')
  })
})
