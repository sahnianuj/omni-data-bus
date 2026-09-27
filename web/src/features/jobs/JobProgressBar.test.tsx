import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import '@/i18n'
import { TooltipProvider } from '@radix-ui/react-tooltip'
import { snapshot } from '@/test/jobs'
import type { JobStatus } from './api'
import { barTone, JobProgressBar } from './JobProgressBar'

const renderBar = (s = snapshot(), status: JobStatus = 'running') =>
  render(
    <TooltipProvider>
      <JobProgressBar snapshot={s} status={status} />
    </TooltipProvider>,
  )

describe('JobProgressBar', () => {
  it('AC1: width, label, aria', () => {
    renderBar()
    const bar = screen.getByRole('progressbar', { name: 'Migration progress' })
    expect(bar).toHaveAttribute('aria-valuenow', '33')
    expect(bar).toHaveAttribute('aria-valuetext', '33 percent, about 37s remaining')
    expect(screen.getByTestId('progress-fill')).toHaveStyle({ width: '33.41%' })
    expect(screen.getByText('33.4 %')).toBeInTheDocument()
    expect(screen.getByText(/402,113 of 1,203,441 rows/)).toHaveTextContent('402,113 of 1,203,441 rows (exact)')
    expect(screen.getByText('ETA 37s · elapsed 19s')).toBeInTheDocument()
  })

  it('AC1: estimate label with tooltip', async () => {
    renderBar(snapshot({ totalKind: 'estimate', etaSeconds: null }))
    const est = screen.getByText('(estimate)')
    await userEvent.hover(est)
    expect((await screen.findAllByText(/From PostgreSQL statistics/))[0]).toBeInTheDocument()
    expect(screen.getByText('elapsed 19s')).toBeInTheDocument()
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuetext', '33 percent')
  })

  it('AC2: color per status', () => {
    const want: Record<JobStatus, string> = {
      queued: 'primary', running: 'primary', pausing: 'primary', paused: 'primary', cancelling: 'muted-foreground', cancelled: 'muted-foreground',
      completed: 'success-dot', completed_with_errors: 'warning-dot', failed: 'danger',
    }
    for (const [status, token] of Object.entries(want) as [JobStatus, string][]) {
      const { unmount } = renderBar(snapshot(), status)
      const fill = screen.getByTestId('progress-fill')
      expect(fill.className).toContain(`bg-[var(--${token})]`)
      expect(fill.className.includes('odb-stripes')).toBe(status === 'running')
      expect(barTone[status]).toContain(token)
      unmount()
    }
  })
})
