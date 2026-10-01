import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { job } from '@/test/jobs'
import { mappingView, sampleSpec } from '@/test/mapping'
import { server, setupMswServer } from '@/test/msw'
import { renderUI } from '@/test/render'
import { mockSession } from '@/test/session'
import { documentUrl } from './api'
import { keyColumns, OutputBrowser, renderKey } from './OutputBrowser'

setupMswServer()

const dirMapping = () =>
  mappingView({
    id: 'm1', projectId: 'p1', sourceTable: 'orders',
    location: { kind: 'directory', folder: 'orders' },
    spec: { ...sampleSpec(), key: { template: '{order_id}::{line_no}' } },
  })

beforeEach(() => {
  mockSession('owner')
  server.use(
    http.get('*/api/v1/projects/:pid/mappings/:mid', () => HttpResponse.json(dirMapping())),
    http.get('*/api/v1/projects/:pid/mappings/:mid/documents/*', ({ request }) => {
      const key = decodeURIComponent(new URL(request.url).pathname.split('/documents/')[1]!)
      if (key !== '1001::3') return HttpResponse.json({ title: 'Document not found', code: 'DOCUMENT_NOT_FOUND', status: 404 }, { status: 404 })
      return HttpResponse.json({ key, path: 'orders/1001/3.json', bytes: 42, modifiedAt: '2026-11-02T14:05:11Z', document: { order: 1001, line: 3 }, location: { kind: 'directory', folder: 'orders' } })
    }),
    http.get('*/api/v1/jobs/:jid/output', ({ request }) => {
      const cursor = new URL(request.url).searchParams.get('cursor')
      return HttpResponse.json(
        cursor
          ? { mappingId: 'm1', location: { kind: 'directory', folder: 'orders' }, items: [{ key: '1002::1', path: 'orders/1002/1.json', bytes: 40 }], nextCursor: '' }
          : { mappingId: 'm1', location: { kind: 'directory', folder: 'orders' }, items: [{ key: '1001::3', path: 'orders/1001/3.json', bytes: 42 }], nextCursor: '1001::3' },
      )
    }),
  )
})

const render = () => renderUI(<OutputBrowser job={job({ id: 'j1', projectId: 'p1', status: 'completed' })} mappings={[{ mappingId: 'm1', table: 'shop.orders' }]} />)

describe('OutputBrowser', () => {
  it('AC4: finds by key and shows path', async () => {
    const user = userEvent.setup()
    render()
    // One input per key-template column.
    await user.type(await screen.findByLabelText('order_id'), '1001')
    await user.type(screen.getByLabelText('line_no'), '3')
    await user.click(screen.getByRole('button', { name: 'Find' }))
    const doc = await screen.findByRole('article', { name: 'Document' })
    expect(within(doc).getByText('orders/1001/3.json')).toBeInTheDocument()
    expect(within(doc).getByText(/"order": 1001/)).toBeInTheDocument()
    expect(within(doc).getByRole('link', { name: 'Download' })).toHaveAttribute('href', documentUrl('p1', 'm1', '1001::3', true))
  })

  it('AC4: not found state', async () => {
    const user = userEvent.setup()
    render()
    await user.type(await screen.findByLabelText('order_id'), '1001')
    await user.type(screen.getByLabelText('line_no'), '9')
    await user.click(screen.getByRole('button', { name: 'Find' }))
    expect(await screen.findByText('No document with key 1001::9')).toBeInTheDocument()
  })

  it('browses a directory page by page and opens a file', async () => {
    const user = userEvent.setup()
    render()
    const files = await screen.findByRole('region', { name: 'Files' })
    await user.click(await within(files).findByRole('button', { name: 'Next page' }))
    await waitFor(() => expect(within(files).getByText('orders/1002/1.json')).toBeInTheDocument())
    await user.click(within(files).getByRole('button', { name: 'First page' }))
    await user.click(await within(files).findByText('orders/1001/3.json'))
    expect(await screen.findByRole('article', { name: 'Document' })).toBeInTheDocument()
  })

  it('key helpers mirror the server', () => {
    expect(keyColumns('{order_id}::{line_no}')).toEqual(['order_id', 'line_no'])
    expect(keyColumns('{$uuid}')).toEqual([])
    expect(renderKey('order::{order_id}', ['7'])).toBe('order::7')
    expect(documentUrl('p1', 'm1', 'a/b c')).toBe('/api/v1/projects/p1/mappings/m1/documents/a/b%20c')
  })
})
