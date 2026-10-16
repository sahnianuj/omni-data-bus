import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryRouter, RouterProvider } from 'react-router'
import { describe, expect, it } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { ErrorState } from './ErrorState'

function at(path: string, error: unknown) {
  const router = createMemoryRouter(
    [
      { path: '/', element: <p>home page</p> },
      { path: '/projects', element: <ErrorState error={error} /> },
    ],
    { initialEntries: [path] },
  )
  render(<RouterProvider router={router} />)
}

describe('ErrorState', () => {
  it('offers the way home on a Bad Gateway', async () => {
    at('/projects', new ApiError(502, { title: 'Bad Gateway' }))
    expect(screen.getByRole('heading', { name: 'Bad Gateway' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('link', { name: 'Back to dashboard' }))
    expect(await screen.findByText('home page')).toBeInTheDocument()
  })

  it('offers the way home when the server is unreachable', () => {
    at('/projects', new TypeError('Failed to fetch'))
    expect(screen.getByRole('link', { name: 'Back to dashboard' })).toBeInTheDocument()
  })

  it("keeps the user in place for a request they can fix", () => {
    at('/projects', new ApiError(400, { title: 'Invalid name' }))
    expect(screen.queryByRole('link', { name: 'Back to dashboard' })).not.toBeInTheDocument()
  })

  it('renders outside a router without the link', () => {
    render(<ErrorState error={new ApiError(502, { title: 'Bad Gateway' })} />)
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
  })
})
