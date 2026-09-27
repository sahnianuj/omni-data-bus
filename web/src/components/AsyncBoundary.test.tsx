import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { AsyncBoundary } from './AsyncBoundary'

type Q = Parameters<typeof AsyncBoundary<string[]>>[0]['query']
const q = (over: Partial<Q>): Q => ({ status: 'success', data: [], error: null, refetch: vi.fn(), ...over }) as Q

describe('AsyncBoundary', () => {
  it('AC4: pending shows skeleton', () => {
    render(<AsyncBoundary query={q({ status: 'pending', data: undefined })}>{() => 'data'}</AsyncBoundary>)
    expect(screen.getByLabelText('Loading…')).toHaveAttribute('aria-busy', 'true')
    expect(screen.queryByText('data')).not.toBeInTheDocument()
  })

  it('AC4: error shows problem and retry calls refetch', async () => {
    const refetch = vi.fn()
    const error = new ApiError(503, { title: 'Service unavailable', detail: 'Metadata store down', code: 'UNAVAILABLE' }, 'req-42')
    render(<AsyncBoundary query={q({ status: 'error', error, refetch })}>{() => 'data'}</AsyncBoundary>)
    expect(screen.getByRole('alert')).toHaveTextContent('Service unavailable')
    expect(screen.getByText('Metadata store down')).toBeInTheDocument()
    expect(screen.getByText(/req-42/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(refetch).toHaveBeenCalledOnce()
  })

  it('AC4: empty renders EmptyState', () => {
    render(
      <AsyncBoundary query={q({ data: [] })} isEmpty={(d) => d.length === 0} empty={<p>nothing here</p>}>
        {() => 'data'}
      </AsyncBoundary>,
    )
    expect(screen.getByText('nothing here')).toBeInTheDocument()
  })

  it('AC4: success renders children with data', () => {
    render(
      <AsyncBoundary query={q({ data: ['a', 'b'] })} isEmpty={(d) => d.length === 0} empty="none">
        {(d) => d.join(',')}
      </AsyncBoundary>,
    )
    expect(screen.getByText('a,b')).toBeInTheDocument()
  })
})
