import { act, screen, waitFor, within } from '@testing-library/react'
import axe from 'axe-core'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { routes } from '@/app/routes'
import { FakeEventSource, withFakeEventSource } from '@/test/eventSource'
import { job, snapshot } from '@/test/jobs'
import { server, setupMswServer } from '@/test/msw'
import { makeProject, mockProject, repo } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'
import type { Job, JobStatus } from './api'

setupMswServer()
withFakeEventSource()

function mockJob(j: Job) {
  server.use(
    http.get('*/api/v1/jobs/:jid', () => HttpResponse.json(j)),
    http.get('*/api/v1/jobs/:jid/events/log', () => HttpResponse.json({ items: [] })),
    http.get('*/api/v1/jobs/:jid/errors/summary', () => HttpResponse.json({ items: [], truncated: false })),
    http.get('*/api/v1/jobs/:jid/errors', () => HttpResponse.json({ items: [], nextCursor: '' })),
  )
}

beforeEach(() => {
  mockSession('owner')
  mockProject(makeProject({ source: repo('postgres'), target: repo('couchbase'), mappings: 2, readyMappings: 2, latestJob: { id: 'j1', status: 'running' } }))
})
afterEach(() => {
  document.title = 'Omni-Data-Bus'
})

const open = () => renderRoutes(routes, '/projects/p1/jobs/j1')
const tile = (name: string) => screen.getByRole('region', { name })

describe('JobMonitorPage', () => {
  it('AC3: updates on events', async () => {
    mockJob(job())
    open()
    expect(await screen.findByText('33.4 %')).toBeInTheDocument()
    const es = FakeEventSource.latest()
    act(() => es.open())
    act(() => es.emit('progress', snapshot({ seq: 2, percent: 40, processed: 481_376, written: 481_253, throughput: 22_000 })))
    // React Query notifies observers asynchronously.
    expect(await screen.findByText('40.0 %')).toBeInTheDocument()
    expect(tile('Written')).toHaveTextContent('481,253')
    act(() =>
      es.emit('progress', snapshot({ seq: 3, percent: 41, throughput: 23_000,
        mappings: [snapshot().mappings[0]!, { ...snapshot().mappings[1]!, processed: 300_000, percent: 27.7 }] })),
    )
    await waitFor(() => expect(within(screen.getAllByRole('list', { name: 'Tables' })[0]!).getAllByRole('listitem')[1]).toHaveTextContent('300,000'))
    expect(within(tile('Throughput')).getByRole('img')).toHaveAccessibleName(/over the last 3 seconds/)
    await waitFor(() => expect(document.title).toBe('41% · Omni-Data-Bus'))
  })

  it('AC3: reconnecting indicator', async () => {
    mockJob(job())
    open()
    await screen.findByText('33.4 %')
    const es = FakeEventSource.latest()
    act(() => es.open())
    expect(screen.getByRole('status')).toHaveTextContent('Live')
    act(() => es.fail())
    expect(screen.getByRole('status')).toHaveTextContent('Reconnecting…')
  })

  it('AC6: title and notification on completion', async () => {
    const notes: [string, NotificationOptions | undefined][] = []
    class FakeNotification {
      static permission = 'granted'
      static requestPermission = vi.fn()
      constructor(title: string, opts?: NotificationOptions) {
        notes.push([title, opts])
      }
    }
    vi.stubGlobal('Notification', FakeNotification)
    mockJob(job())
    open()
    await screen.findByText('33.4 %')
    const done = snapshot({ seq: 9, status: 'completed', percent: 100, processed: 1_203_441, written: 1_203_441, failed: 0, elapsedSeconds: 58 })
    mockJob(job({ status: 'completed', progress: done }))
    act(() => FakeEventSource.latest().emit('completed', done))
    await waitFor(() => expect(document.title).toBe('✓ Done · Omni-Data-Bus'))
    expect(await screen.findByRole('region', { name: 'Migration summary' })).toHaveTextContent('Migrated 1,203,441 documents in 58s')
    expect(notes).toEqual([['Migration finished', { body: 'Completed: 1,203,441 documents written.' }]])
    expect(FakeEventSource.latest().readyState).toBe(2)
  })

  it('AC8: axe clean in each state', async () => {
    const states: JobStatus[] = ['running', 'paused', 'completed', 'completed_with_errors', 'failed']
    for (const status of states) {
      mockJob(job({ status, progress: snapshot({ status }), resumable: status === 'failed' }))
      const { unmount } = open()
      await screen.findByRole('progressbar')
      const res = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false }, region: { enabled: false } } })
      const serious = res.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical').map((v) => `${v.id}: ${v.nodes[0]?.html}`)
      expect(serious, status).toEqual([])
      unmount()
    }
  }, 20_000) // five axe runs; slow under coverage instrumentation
})
