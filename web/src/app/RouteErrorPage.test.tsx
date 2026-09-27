import { screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Outlet } from 'react-router'
import { renderRoutes } from '@/test/render'
import { RouteErrorPage } from './RouteErrorPage'

function Boom(): never {
  throw new TypeError("Cannot read properties of null (reading 'scrollTop')")
}

describe('RouteErrorPage', () => {
  afterEach(() => vi.restoreAllMocks())

  it('replaces the router default screen with a recoverable page', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    renderRoutes([{ path: '/', element: <Boom />, errorElement: <RouteErrorPage fullPage /> }])
    expect(await screen.findByRole('heading', { name: 'This page ran into a problem' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /reload page/i })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Back to dashboard' })).toBeInTheDocument()
    expect(screen.queryByText(/Unexpected Application Error/)).not.toBeInTheDocument()
  })

  it('keeps the surrounding shell when a nested page crashes', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    renderRoutes(
      [
        {
          path: '/',
          element: (
            <>
              <nav>shell nav</nav>
              <Outlet />
            </>
          ),
          children: [{ errorElement: <RouteErrorPage />, children: [{ path: 'x', element: <Boom /> }] }],
        },
      ],
      '/x',
    )
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(screen.getByText('shell nav')).toBeInTheDocument()
  })
})
