import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { job, snapshot } from '@/test/jobs'
import { server, setupMswServer } from '@/test/msw'
import { renderUI } from '@/test/render'
import { mappingReport, report, verification } from '@/test/report'
import { JobSummaryCard } from './JobSummaryCard'

setupMswServer()

describe('JobSummaryCard', () => {
  it('AC6: renders final metrics and actions', async () => {
    server.use(http.get('/api/v1/jobs/j1/report', () => HttpResponse.json(report([mappingReport('m1', 'customer', verification())]))))
    const onViewErrors = vi.fn()
    renderUI(
      <JobSummaryCard
        job={job({ status: 'completed_with_errors', progress: snapshot({ status: 'completed_with_errors', written: 1_203_318, failed: 123, skipped: 0, elapsedSeconds: 58, percent: 100 }) })}
        onViewErrors={onViewErrors}
      />,
    )
    const card = await screen.findByRole('region', { name: 'Migration summary' })
    expect(card).toHaveTextContent('Migrated 1,203,318 documents in 58s (avg 20.7k/s)')
    expect(card).toHaveTextContent('0 skipped · 123 failed')
    expect(screen.getByRole('link', { name: 'Download report' })).toHaveAttribute('href', '/api/v1/jobs/j1/report.html?download=1')
    expect(await screen.findByTestId('verify-m1')).toHaveTextContent('100/100 samples match')
    await userEvent.click(screen.getByRole('button', { name: 'More report formats' }))
    expect(await screen.findByRole('link', { name: 'JSON' })).toHaveAttribute('href', '/api/v1/jobs/j1/report?download=1')
    expect(screen.getByRole('link', { name: 'CSV (one row per table)' })).toHaveAttribute('href', '/api/v1/jobs/j1/report.csv?download=1')
    expect(screen.getByRole('link', { name: 'HTML (print to PDF)' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Download CSV' })).toHaveAttribute('href', '/api/v1/jobs/j1/errors/export?format=csv')
    await userEvent.click(screen.getByRole('button', { name: 'View errors' }))
    expect(onViewErrors).toHaveBeenCalled()
  })
})
