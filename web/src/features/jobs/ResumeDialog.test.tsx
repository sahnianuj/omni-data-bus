import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { job } from '@/test/jobs'
import { server, setupMswServer } from '@/test/msw'
import { renderUI } from '@/test/render'
import { mockSession } from '@/test/session'
import { JobControls } from './JobControls'
import { ResumeDialog } from './ResumeDialog'

setupMswServer()
beforeEach(() => mockSession('owner'))

function mockResume() {
  const modes: (string | undefined)[] = []
  server.use(
    http.post('*/api/v1/jobs/:jid/resume', async ({ request }) => {
      const body = (await request.json().catch(() => ({}))) as { mode?: string }
      modes.push(body?.mode)
      return HttpResponse.json(job({ id: body?.mode === 'restart' ? 'j2' : 'j1', status: 'queued' }))
    }),
  )
  return modes
}

// Editor-only buttons are enabled once the session has loaded.
async function enabledResume() {
  await waitFor(() => expect(screen.getByRole('button', { name: 'Resume' })).toBeEnabled())
  return screen.getByRole('button', { name: 'Resume' })
}

describe('ResumeDialog', () => {
  it('AC7: warns on changed mapping and no-PK', async () => {
    const modes = mockResume()
    const onResumed = vi.fn()
    const paused = job({
      status: 'paused',
      resumable: true,
      resumeInfo: {
        rowsDone: 402_113,
        changed: [{ mappingId: 'm1', table: 'public.customer' }],
        restart: [{ mappingId: 'm2', table: 'public.audit_log' }],
      },
    })
    renderUI(<ResumeDialog job={paused} open onOpenChange={() => {}} onResumed={onResumed} />)
    expect(screen.getByText("402,113 rows are already done and won't be read again.")).toBeInTheDocument()
    expect(screen.getByText(/Mapping changed since this job started/)).toBeInTheDocument()
    expect(screen.getByText('Changed: public.customer')).toBeInTheDocument()
    expect(screen.getByText('public.audit_log has no primary key; resuming will re-read all of its rows.')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Restart with new mapping' }))
    await waitFor(() => expect(onResumed).toHaveBeenCalledWith(expect.objectContaining({ id: 'j2' }), 'restart'))
    expect(modes).toEqual(['restart'])
  })

  it('AC7: resume keeps the original mapping', async () => {
    const modes = mockResume()
    const onResumed = vi.fn()
    const paused = job({ status: 'paused', resumable: true, resumeInfo: { rowsDone: 10, changed: [{ mappingId: 'm1', table: 'public.customer' }], restart: [] } })
    renderUI(<ResumeDialog job={paused} open onOpenChange={() => {}} onResumed={onResumed} />)
    await userEvent.click(screen.getByRole('button', { name: 'Resume with original mapping' }))
    await waitFor(() => expect(onResumed).toHaveBeenCalledWith(expect.objectContaining({ id: 'j1' }), 'original'))
    expect(modes).toEqual(['original'])
  })

  it('opens from the controls only when there is something to decide', async () => {
    const modes = mockResume()
    const onRetried = vi.fn()
    const { unmount } = renderUI(<JobControls job={job({ status: 'paused', resumable: true, resumeInfo: { rowsDone: 5, changed: [], restart: [] } })} />)
    await userEvent.click(await enabledResume())
    await waitFor(() => expect(modes).toHaveLength(1))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    unmount()

    renderUI(
      <JobControls
        onRetried={onRetried}
        job={job({ status: 'paused', resumable: true, resumeInfo: { rowsDone: 5, changed: [{ mappingId: 'm1', table: 'public.customer' }], restart: [] } })}
      />,
    )
    await userEvent.click(await enabledResume())
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Restart with new mapping' }))
    await waitFor(() => expect(onRetried).toHaveBeenCalledWith('j2'), { timeout: 2000 })
  })
})
