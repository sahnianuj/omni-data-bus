import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { useState } from 'react'
import { beforeEach, describe, expect, it } from 'vitest'
import { job } from '@/test/jobs'
import { server, setupMswServer } from '@/test/msw'
import { renderUI } from '@/test/render'
import { mockSession } from '@/test/session'
import type { Job, JobStatus } from './api'
import { JobControls } from './JobControls'

setupMswServer()
beforeEach(() => mockSession('owner'))

async function buttons() {
  // Editor-only buttons appear once the session has loaded.
  await waitFor(() => expect(screen.queryAllByRole('button').every((b) => !b.hasAttribute('disabled') || b.textContent === 'Pausing…')).toBe(true))
  return screen.queryAllByRole('button').map((b) => b.textContent)
}

function mockControl() {
  const calls: string[] = []
  server.use(
    http.post('*/api/v1/jobs/:jid/:action', ({ params }) => {
      calls.push(String(params.action))
      const next: Record<string, JobStatus> = { pause: 'pausing', resume: 'queued', cancel: 'cancelling' }
      return HttpResponse.json(job({ status: next[String(params.action)] }))
    }),
  )
  return calls
}

describe('JobControls', () => {
  it('AC2: controls per status (table-driven)', async () => {
    const cases: [Partial<Job>, (string | null)[], string[]][] = [
      [{ status: 'running' }, ['Pause', 'Cancel'], []],
      [{ status: 'pausing' }, ['Pausing…'], []],
      [{ status: 'paused' }, ['Resume', 'Cancel'], []],
      [{ status: 'queued' }, ['Cancel'], ['Waiting for a worker…']],
      [{ status: 'failed', resumable: true, retryable: true }, ['Resume', 'Retry failed rows', 'Run again'], []],
      [{ status: 'failed', resumable: false, retryable: false }, ['Run again'], []],
      [{ status: 'completed' }, ['Run again'], []],
      [{ status: 'completed_with_errors' }, ['Run again'], []],
      [{ status: 'cancelled' }, ['Run again'], []],
    ]
    for (const [over, want, texts] of cases) {
      const { unmount } = renderUI(<JobControls job={job(over)} />)
      await waitFor(async () => expect(await buttons()).toEqual(want.filter((w) => w !== 'Run again')))
      if (want.includes('Run again')) expect(screen.getByRole('link', { name: 'Run again' })).toHaveAttribute('href', '/projects/p1/review')
      for (const text of texts) expect(screen.getByText(text)).toBeInTheDocument()
      unmount()
    }
  })

  it('AC4: pause optimistic state', async () => {
    const calls = mockControl()
    let setStatus: (s: JobStatus) => void = () => {}
    function Harness() {
      const [status, set] = useState<JobStatus>('running')
      setStatus = set
      return <JobControls job={job({ status })} />
    }
    renderUI(<Harness />)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Pause' })).toBeEnabled())
    await userEvent.click(screen.getByRole('button', { name: 'Pause' }))
    expect(screen.getByRole('button', { name: 'Pausing…' })).toBeDisabled()
    await waitFor(() => expect(calls).toEqual(['pause']))
    // Still "Pausing…" until the status event arrives.
    expect(screen.getByRole('button', { name: 'Pausing…' })).toBeInTheDocument()
    act(() => setStatus('paused')) // the status event arrives
    expect(await screen.findByRole('button', { name: 'Resume' })).toBeInTheDocument()
  })

  it('AC4: cancel confirm', async () => {
    const calls = mockControl()
    renderUI(<JobControls job={job({ status: 'running' })} />)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Cancel' })).toBeEnabled())
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    const dialog = await screen.findByRole('dialog', { name: 'Cancel the migration?' })
    expect(dialog).toHaveTextContent('Documents already written stay in the target.')
    expect(calls).toEqual([])
    await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel migration' }))
    await waitFor(() => expect(calls).toEqual(['cancel']))
  })

  it('AC4: resume', async () => {
    const calls = mockControl()
    renderUI(<JobControls job={job({ status: 'paused', resumable: true })} />)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Resume' })).toBeEnabled())
    await userEvent.click(screen.getByRole('button', { name: 'Resume' }))
    await waitFor(() => expect(calls).toEqual(['resume']))
  })
})
