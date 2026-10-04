import { screen, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { server, setupMswServer } from '@/test/msw'
import { renderUI } from '@/test/render'
import type { ExportRecord } from './api'
import { ExportHistory } from './ExportHistory'

setupMswServer()

const record = (id: string, over: Partial<ExportRecord> = {}): ExportRecord => ({
  id, projectId: 'p1', createdBy: 'u1', createdByEmail: 'ada@example.com', createdAt: new Date().toISOString(), mappingIds: ['m1', 'm2'],
  planHashes: {}, targetType: 'directory', credentialMode: 'sealed', runtime: 'script', odbVersion: '2.0.0',
  bundleSha256: id.repeat(64).slice(0, 64), planHash: 'p', changed: [], ...over,
})

describe('ExportHistory', () => {
  it('AC3: changed badge', async () => {
    server.use(
      http.get('*/api/v1/projects/p1/exports', () =>
        HttpResponse.json({ items: [record('a', { changed: ['public.orders'] }), record('b', { runtime: 'bundle', targetType: 'couchbase' })] }),
      ),
    )
    renderUI(<ExportHistory projectId="p1" />)
    const rows = (await screen.findAllByRole('row')).slice(1)
    expect(rows).toHaveLength(2)
    const first = within(rows[0]!)
    expect(first.getByText('Changed since export')).toHaveAttribute('title', expect.stringContaining('public.orders'))
    expect(first.getByText('Folder')).toBeInTheDocument()
    expect(first.getByText('aaaaaaaaaaaa')).toBeInTheDocument()
    const second = within(rows[1]!)
    expect(second.getByText('Current')).toBeInTheDocument()
    expect(second.getByText('Couchbase')).toBeInTheDocument()
    expect(second.getByText('.odb.json')).toBeInTheDocument()
  })
})
