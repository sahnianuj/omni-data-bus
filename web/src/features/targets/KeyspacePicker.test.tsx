import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { bucketList, caps, mockKeyspaces } from '@/test/keyspaces'
import { server, setupMswServer } from '@/test/msw'
import { renderUI } from '@/test/render'
import { emptyKeyspace, KeyspacePicker, type KeyspaceValue } from './KeyspacePicker'

setupMswServer()

let last: KeyspaceValue = emptyKeyspace
function Harness({ initial = emptyKeyspace, allowDeferred }: { initial?: KeyspaceValue; allowDeferred?: boolean }) {
  const [v, setV] = useState(initial)
  return (
    <KeyspacePicker
      connId="p1::target"
      value={v}
      allowDeferred={allowDeferred}
      onChange={(n) => {
        last = n
        setV(n)
      }}
    />
  )
}

async function pick(label: string, option: string) {
  await userEvent.click(screen.getByRole('combobox', { name: label }))
  await userEvent.click(await screen.findByRole('option', { name: new RegExp(`^${option}`) }))
}

describe('KeyspacePicker', () => {
  it('AC1: manual bucket entry when partial & empty', async () => {
    mockKeyspaces(bucketList({ buckets: [], partial: true, capabilities: caps({ canCreateBucket: false, deployment: 'capella', reasons: ['Create buckets in the Capella UI, then pick them here.'] }) }))
    renderUI(<Harness />)
    const input = await screen.findByPlaceholderText('Type a bucket name')
    expect(screen.getByRole('button', { name: 'Use' })).toBeDisabled()
    await userEvent.type(input, '.bad')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    await userEvent.clear(input)
    await userEvent.type(input, 'travel-sample')
    await userEvent.click(screen.getByRole('button', { name: 'Use' }))
    expect(last.bucket).toBe('travel-sample')
    // The scopes of the typed bucket load, which confirms it exists.
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Scope' })).toBeEnabled())
  })

  it('AC2: cascading reset on bucket change', async () => {
    mockKeyspaces()
    renderUI(<Harness />)
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Bucket' })).toHaveTextContent('Select a bucket'))
    expect(screen.getByRole('combobox', { name: 'Scope' })).toBeDisabled()
    await pick('Bucket', 'travel-sample')
    await pick('Scope', 'inventory')
    await pick('Collection', 'route')
    expect(last).toEqual({ bucket: 'travel-sample', scope: 'inventory', collection: 'route', createIfMissing: false })
    expect(screen.getByText('`travel-sample`.`inventory`.`route`')).toBeInTheDocument()

    await pick('Bucket', 'archive')
    expect(last).toEqual({ bucket: 'archive', scope: '', collection: '' })
    expect(screen.getByRole('combobox', { name: 'Collection' })).toBeDisabled()
  })

  it('AC3: disables actions per capabilities', async () => {
    mockKeyspaces(bucketList({ capabilities: caps({ canCreateBucket: false, reasons: ['Create buckets in the Capella UI, then pick them here.'] }) }))
    renderUI(<Harness />)
    expect(await screen.findByLabelText('New bucket: Create buckets in the Capella UI, then pick them here.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'New bucket' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'New scope' })).toBeDisabled()

    await pick('Bucket', 'archive') // canManage: false
    expect(screen.getByLabelText("New scope: This credential can't create scopes or collections in this bucket.")).toBeInTheDocument()
    await pick('Scope', '_default')
    expect(screen.getByRole('button', { name: 'New collection' })).toBeDisabled()

    await pick('Bucket', 'travel-sample') // canManage: true
    expect(screen.getByRole('button', { name: 'New scope' })).toBeEnabled()
  })

  it('AC4: shows overwrite notice when count > 0', async () => {
    mockKeyspaces(bucketList(), { route: 187, airline: 0 })
    renderUI(<Harness initial={{ bucket: 'travel-sample', scope: 'inventory', collection: 'route' }} />)
    expect(await screen.findByText('~187 docs')).toBeInTheDocument()
    expect(screen.getByRole('note')).toHaveTextContent('Collection is not empty')

    await pick('Collection', 'airline')
    expect(await screen.findByText('~0 docs')).toBeInTheDocument()
    expect(screen.queryByRole('note')).not.toBeInTheDocument()
  })

  it('AC4: no badge and no error when the count is unknown', async () => {
    mockKeyspaces(bucketList(), {})
    renderUI(<Harness initial={{ bucket: 'travel-sample', scope: 'inventory', collection: 'route' }} />)
    expect(await screen.findByText('`travel-sample`.`inventory`.`route`')).toBeInTheDocument()
    expect(screen.queryByText(/docs$/)).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('AC5: deferred create sets flag and badge', async () => {
    mockKeyspaces()
    let created = false
    server.use(http.post('*/api/v1/connections/:id/buckets/:bucket/scopes/:scope/collections', () => ((created = true), HttpResponse.json({}))))
    renderUI(<Harness allowDeferred initial={{ bucket: 'travel-sample', scope: 'inventory', collection: '' }} />)
    await userEvent.click(await screen.findByRole('button', { name: 'New collection' }))
    await userEvent.type(screen.getByLabelText('Name'), 'orders')
    await userEvent.click(screen.getByLabelText(/Create when migration starts/))
    await userEvent.click(screen.getByRole('button', { name: 'Use this name' }))
    expect(last).toEqual({ bucket: 'travel-sample', scope: 'inventory', collection: 'orders', createIfMissing: true })
    expect(screen.getByText('will be created')).toBeInTheDocument()
    expect(created).toBe(false)
  })

  it('shows the buckets error with a retry', async () => {
    server.use(http.get('*/api/v1/connections/:id/buckets', () => HttpResponse.json({ status: 502, code: 'TARGET_UNREACHABLE', title: 'Host unreachable' }, { status: 502 })))
    renderUI(<Harness />)
    expect((await screen.findAllByText('Host unreachable')).length).toBeGreaterThan(0)
  })
})
