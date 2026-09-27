import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { server, setupMswServer } from '@/test/msw'
import { renderUI } from '@/test/render'
import { mockSession } from '@/test/session'
import type { AuditEvent } from './api'
import { AuditLogPage } from './AuditLogPage'
import { SettingsPage } from './SettingsPage'

setupMswServer()

const ev = (i: number, action: string, userId = 'u1', metadata?: Record<string, string>): AuditEvent => ({
  id: `e${i}`, workspaceId: 'w1', userId, action, entityType: action.split('.')[0]!, entityId: `id-${i}`, metadata,
  createdAt: new Date(Date.now() - i * 60_000).toISOString(),
})

describe('AuditLogPage', () => {
  it('AC7: renders and filters', async () => {
    mockSession('owner')
    const seen: URLSearchParams[] = []
    server.use(
      http.get('*/api/v1/audit', ({ request }) => {
        const q = new URL(request.url).searchParams
        seen.push(q)
        if (q.get('cursor') === 'c2') return HttpResponse.json({ items: [ev(9, 'project.create')], nextCursor: '' })
        if (q.get('action') === 'job.') return HttpResponse.json({ items: [ev(1, 'job.create', 'u1', { projectId: 'p1' })], nextCursor: '' })
        return HttpResponse.json({
          items: [ev(1, 'job.create', 'u1', { projectId: 'p1' }), ev(2, 'connection.test', 'u2-other-user'), ev(3, 'report.download', 'u1', { format: 'csv' })],
          nextCursor: 'c2',
        })
      }),
    )
    renderUI(<AuditLogPage />)
    const table = await screen.findByRole('table')
    const rows = within(table).getAllByRole('row')
    expect(rows).toHaveLength(4) // header + 3
    expect(rows[1]).toHaveTextContent('You')
    expect(rows[1]).toHaveTextContent('job.create')
    expect(rows[1]).toHaveTextContent('projectId=p1')
    expect(rows[2]).toHaveTextContent('u2-other') // other users by short ID
    expect(rows[3]).toHaveTextContent('format=csv')

    await userEvent.click(screen.getByRole('button', { name: 'Load more' }))
    expect(await within(table).findByText('project.create')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument()

    await userEvent.selectOptions(screen.getByLabelText('Action'), 'Migrations')
    await screen.findByText('projectId=p1')
    expect(await within(await screen.findByRole('table')).findAllByRole('row')).toHaveLength(2)
    expect(seen.at(-1)?.get('action')).toBe('job.')

    await userEvent.click(screen.getByLabelText('Only my actions'))
    await screen.findByRole('table')
    expect(seen.at(-1)?.get('userId')).toBe('u1')
  })

  it('AC7: editors see that the log is for owners, and no request is made', async () => {
    mockSession('editor')
    let called = false
    server.use(http.get('*/api/v1/audit', () => ((called = true), HttpResponse.json({ items: [], nextCursor: '' }))))
    renderUI(<AuditLogPage />)
    expect(await screen.findByText('Only workspace owners can see the audit log')).toBeInTheDocument()
    expect(called).toBe(false)
  })

  it('settings links owners to the audit log', async () => {
    mockSession('owner')
    renderUI(<SettingsPage />)
    expect(await screen.findByRole('link', { name: /Audit log/ })).toHaveAttribute('href', '/settings/audit')
  })
})
