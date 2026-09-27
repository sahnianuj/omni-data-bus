import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { server, setupMswServer } from '@/test/msw'
import { renderUI } from '@/test/render'
import { pendingPoll } from './api'
import { NewBucketDialog, NewNamePopover } from './NewKeyspaceControls'

setupMswServer()

const collectionsURL = '*/api/v1/connections/:id/buckets/:bucket/scopes/:scope/collections'

async function createCollection(name: string, ttl = '') {
  await userEvent.click(screen.getByRole('button', { name: 'New collection' }))
  await userEvent.type(screen.getByLabelText('Name'), name)
  if (ttl) await userEvent.type(screen.getByLabelText('Max TTL (optional)'), ttl)
  await userEvent.click(screen.getByRole('button', { name: 'Create' }))
}

describe('NewNamePopover', () => {
  it('AC3: selects existing on 409', async () => {
    server.use(http.post(collectionsURL, () => HttpResponse.json({ status: 409, code: 'KEYSPACE_EXISTS', title: 'Collection already exists' }, { status: 409 })))
    const onDone = vi.fn()
    renderUI(<NewNamePopover connId="c" kind="collection" bucket="b" scope="s" onDone={onDone} />)
    await createCollection('route')
    expect(onDone).toHaveBeenCalledWith('route', false)
    expect(await screen.findByText('Collection route already exists — selected it')).toBeInTheDocument()
  })

  it('AC2: sends the parsed TTL and waits out a 202', async () => {
    pendingPoll.ms = 1
    let body: unknown
    let polls = 0
    server.use(
      http.post(collectionsURL, async ({ request }) => ((body = await request.json()), HttpResponse.json({ status: 'pending', bucket: 'b', scope: 's', collection: 'orders' }, { status: 202 }))),
      http.get(`${collectionsURL}/:collection`, () => (++polls < 2 ? HttpResponse.json({ status: 404, code: 'NOT_FOUND', title: 'x' }, { status: 404 }) : HttpResponse.json({ name: 'orders', maxExpirySec: 2592000 }))),
    )
    const onDone = vi.fn()
    renderUI(<NewNamePopover connId="c" kind="collection" bucket="b" scope="s" onDone={onDone} />)
    await createCollection('orders', '30d')
    expect(await screen.findByText('Collection orders created')).toBeInTheDocument()
    expect(body).toEqual({ name: 'orders', maxExpirySec: 2592000 })
    expect(onDone).toHaveBeenCalledWith('orders', false)
  })

  it('validates the name and TTL locally, and shows server errors', async () => {
    server.use(http.post('*/api/v1/connections/:id/buckets/:bucket/scopes', () => HttpResponse.json({ status: 422, code: 'TARGET_PERMISSION_DENIED', title: 'Permission denied', detail: "This credential can't create scopes" }, { status: 422 })))
    renderUI(<NewNamePopover connId="c" kind="scope" bucket="b" onDone={() => {}} />)
    await userEvent.click(screen.getByRole('button', { name: 'New scope' }))
    await userEvent.type(screen.getByLabelText('Name'), '_bad')
    await userEvent.click(screen.getByRole('button', { name: 'Create' }))
    expect(screen.getByRole('alert')).toHaveTextContent('not starting with')
    await userEvent.clear(screen.getByLabelText('Name'))
    await userEvent.type(screen.getByLabelText('Name'), 'tenant')
    await userEvent.click(screen.getByRole('button', { name: 'Create' }))
    expect(await screen.findByText("This credential can't create scopes")).toBeInTheDocument()
  })

  it('rejects an invalid TTL', async () => {
    renderUI(<NewNamePopover connId="c" kind="collection" bucket="b" scope="s" onDone={() => {}} />)
    await createCollection('orders', 'forever')
    expect(screen.getByRole('alert')).toHaveTextContent('Enter 0 or a duration')
  })
})

describe('NewBucketDialog', () => {
  it('validates magma RAM and creates the bucket', async () => {
    let body: unknown
    server.use(http.post('*/api/v1/connections/:id/buckets', async ({ request }) => ((body = await request.json()), HttpResponse.json({ status: 'ready', bucket: 'orders' }, { status: 201 }))))
    const onDone = vi.fn()
    renderUI(<NewBucketDialog connId="c" onDone={onDone} />)
    await userEvent.click(screen.getByRole('button', { name: 'New bucket' }))
    await userEvent.type(screen.getByLabelText('Name'), 'orders')
    await userEvent.clear(screen.getByLabelText('RAM quota (MB)'))
    await userEvent.type(screen.getByLabelText('RAM quota (MB)'), '512')
    await userEvent.click(screen.getByRole('button', { name: 'Create' }))
    expect(screen.getByText('Magma needs at least 1024 MB')).toBeInTheDocument()
    await userEvent.selectOptions(screen.getByLabelText('Storage backend'), 'couchstore')
    await userEvent.click(screen.getByRole('button', { name: 'Create' }))
    expect(await screen.findByText('Bucket orders created')).toBeInTheDocument()
    expect(body).toEqual({ name: 'orders', bucketType: 'couchbase', ramQuotaMB: 512, numReplicas: 1, storageBackend: 'couchstore' })
    expect(onDone).toHaveBeenCalledWith('orders')
  })

  it('ephemeral buckets send no storage backend; Capella refusals are shown', async () => {
    let body: Record<string, unknown> = {}
    server.use(
      http.post('*/api/v1/connections/:id/buckets', async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ status: 422, code: 'CAPELLA_BUCKET_CREATE_UNSUPPORTED', title: 'Create buckets in Capella', detail: 'Capella buckets are created in the Capella UI.' }, { status: 422 })
      }),
    )
    renderUI(<NewBucketDialog connId="c" onDone={() => {}} />)
    await userEvent.click(screen.getByRole('button', { name: 'New bucket' }))
    await userEvent.type(screen.getByLabelText('Name'), 'cache')
    await userEvent.click(screen.getByLabelText(/^Ephemeral/))
    await userEvent.click(screen.getByRole('button', { name: 'Create' }))
    expect(await screen.findByText('Capella buckets are created in the Capella UI.')).toBeInTheDocument()
    expect(body.storageBackend).toBeUndefined()
    expect(body.bucketType).toBe('ephemeral')
  })
})
