import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { job } from '@/test/jobs'
import { server, setupMswServer } from '@/test/msw'
import { renderUI } from '@/test/render'
import { mockSession } from '@/test/session'
import type { JobError } from './api'
import { JobErrorsTab } from './JobErrorsTab'

setupMswServer()

const err = (seq: number, code: string, over: Partial<JobError> = {}): JobError => ({
  jobId: 'j1', seq, mappingId: 'm2', stage: 'transform', code: code as JobError['code'], message: `"12,5" is not a number (${seq})`,
  fieldPath: 'total', sourceKey: [918270 + seq], sample: { order_id: 918270 + seq, total: '12,5' }, at: new Date().toISOString(), ...over,
})

function mockErrors() {
  const state = { listQueries: [] as string[], retries: [] as unknown[] }
  server.use(
    http.get('*/api/v1/jobs/:jid/errors/summary', () =>
      HttpResponse.json({
        truncated: false,
        items: [
          { stage: 'transform', code: 'PARSE_FAILED', count: 120, example: err(1, 'PARSE_FAILED') },
          { stage: 'write', code: 'DOC_TOO_LARGE', count: 3, example: err(9, 'DOC_TOO_LARGE', { stage: 'write' }) },
        ],
      }),
    ),
    http.get('*/api/v1/jobs/:jid/errors', ({ request }) => {
      const code = new URL(request.url).searchParams.get('code')
      state.listQueries.push(code ?? '')
      const items = code === 'DOC_TOO_LARGE' ? [err(9, 'DOC_TOO_LARGE', { stage: 'write', fieldPath: undefined })] : [err(1, 'PARSE_FAILED'), err(2, 'PARSE_FAILED')]
      return HttpResponse.json({ items, nextCursor: '' })
    }),
    http.post('*/api/v1/jobs/:jid/retry', async ({ request }) => {
      state.retries.push(await request.json())
      return HttpResponse.json({ job: job({ id: 'j2', status: 'queued' }), preflight: { ok: true, checks: [], mappings: [], totalRows: 3 } }, { status: 201 })
    }),
  )
  return state
}

beforeEach(() => mockSession('owner'))

describe('JobErrorsTab', () => {
  it('AC7: summary, filter, drawer', async () => {
    const state = mockErrors()
    renderUI(<JobErrorsTab job={job({ status: 'completed_with_errors', retryable: true })} />)
    const byCode = await screen.findByRole('region', { name: 'Errors by code' })
    expect(within(byCode).getByText("Value couldn't be converted")).toBeInTheDocument()
    expect(within(byCode).getByText('Document is over 20 MB')).toBeInTheDocument()
    expect(within(byCode).getByText('120')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Download CSV' })).toHaveAttribute('href', '/api/v1/jobs/j1/errors/export?format=csv')

    // Clicking a code filters the table.
    expect(await screen.findAllByRole('row')).toHaveLength(3)
    await userEvent.click(within(byCode).getByRole('button', { name: /Document is over 20 MB/ }))
    await waitFor(() => expect(state.listQueries).toContain('DOC_TOO_LARGE'))
    const table = await screen.findByRole('table', { name: 'Failed rows' })
    await waitFor(() => expect(within(table).getAllByRole('row')).toHaveLength(2))
    expect(within(table).getByText('918279')).toBeInTheDocument()

    // The drawer shows the source row and the fix hint.
    await userEvent.click(within(table).getByRole('button', { name: 'View row' }))
    const drawer = await screen.findByRole('dialog', { name: 'Document is over 20 MB' })
    expect(drawer).toHaveTextContent('Split large columns out')
    expect(drawer).toHaveTextContent('"total": "12,5"')
  })

  it('AC7: retry failed rows', async () => {
    const state = mockErrors()
    const { router } = renderUI(<JobErrorsTab job={job({ status: 'completed_with_errors', retryable: true })} />)
    // Editors only: enabled once the session has loaded.
    await waitFor(() => expect(screen.getByRole('button', { name: 'Retry failed rows' })).toBeEnabled())
    await userEvent.click(screen.getByRole('button', { name: 'Retry failed rows' }))
    const dialog = await screen.findByRole('dialog', { name: 'Retry the failed rows?' })
    expect(dialog).toHaveTextContent('uses your current mapping — fix the mapping first if needed')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Retry failed rows' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/projects/p1/jobs/j2'))
    expect(state.retries).toEqual([{}])
  })

  it('no retry button when retry is not possible', async () => {
    mockErrors()
    renderUI(<JobErrorsTab job={job({ status: 'completed_with_errors', retryable: false })} />)
    await screen.findByRole('region', { name: 'Errors by code' })
    expect(screen.queryByRole('button', { name: 'Retry failed rows' })).not.toBeInTheDocument()
  })
})
