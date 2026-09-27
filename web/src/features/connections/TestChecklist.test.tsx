import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { failedResult, okResult } from '@/test/connections'
import { TestChecklist } from './TestChecklist'

describe('TestChecklist', () => {
  it('AC4: renders steps with statuses and hints', () => {
    render(<TestChecklist result={okResult} saved={false} />)
    const list = screen.getByRole('region', { name: 'Connection test' })
    expect(within(list).getByText('Passed with 1 warning')).toBeInTheDocument()
    expect(within(list).getByText('PostgreSQL 16.3')).toBeInTheDocument()
    expect(within(list).getByText(/No SELECT on 3 table/)).toBeInTheDocument()
    expect(within(list).getByText(/GRANT SELECT ON ALL TABLES IN SCHEMA billing TO omni_ro;/)).toBeInTheDocument()
    expect(within(list).getByRole('button', { name: 'Copy' })).toBeInTheDocument()
    expect(within(list).getByText(/nothing saved yet/)).toBeInTheDocument()
  })

  it('AC4: failure shows the reason, hint and skipped steps', () => {
    const { container } = render(<TestChecklist result={failedResult} />)
    expect(screen.getByText('Failed')).toBeInTheDocument()
    expect(screen.getByText('password authentication failed')).toBeInTheDocument()
    expect(screen.getByText('Check the username and password.')).toBeInTheDocument()
    expect(container.querySelectorAll('[data-status="skipped"]')).toHaveLength(2)
  })
})
