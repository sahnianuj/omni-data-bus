import { screen, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { job, snapshot } from '@/test/jobs'
import { server, setupMswServer } from '@/test/msw'
import { renderUI } from '@/test/render'
import { JobsPage } from './JobsPage'

setupMswServer()

const jobs = [
  job({ id: 'run', progress: snapshot({ throughput: 21_450.5, failed: 123 }) }),
  job({ id: 'done', status: 'completed', finishedAt: new Date().toISOString(), progress: snapshot({ status: 'completed', written: 1_000_000, failed: 0, throughput: 0, elapsedSeconds: 20 }) }),
  job({ id: 'queued', status: 'queued', startedAt: undefined, progress: snapshot({ status: 'queued', written: 0, failed: 0, throughput: 0 }) }),
]

async function rows() {
  server.use(
    http.get('/api/v1/jobs', () => HttpResponse.json({ items: jobs })),
    http.get('/api/v1/projects', () => HttpResponse.json({ items: [{ id: 'p1', name: 'Shop' }] })),
  )
  renderUI(<JobsPage />)
  await screen.findAllByRole('link', { name: 'Shop' })
  return screen.getAllByRole('row').slice(1).map((r) => within(r).getAllByRole('cell').map((c) => c.textContent))
}

describe('JobsPage', () => {
  it('shows errors and throughput for running, finished and queued jobs', async () => {
    const tail = (await rows()).map((cells) => cells.slice(5))
    expect(tail[0]).toEqual(['123', '21.5k docs/s'])
    // A finished job reports 0 errors and its average rate, not blanks.
    expect(tail[1]).toEqual(['0', '50.0k docs/s'])
    expect(tail[2]).toEqual(['—', '—'])
  })
})
