import { screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { setupMswServer } from '@/test/msw'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'

setupMswServer()

describe('RequireAuth', () => {
  it('AC8: redirects with next param', async () => {
    mockSession(null)
    const { router } = renderRoutes(routes, '/projects/abc?tab=source')
    await waitFor(() => expect(router.state.location.pathname).toBe('/signin'))
    expect(router.state.location.search).toBe('?next=%2Fprojects%2Fabc%3Ftab%3Dsource')
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeInTheDocument()
  })

  it('AC8: renders the app for signed-in users', async () => {
    mockSession('editor')
    renderRoutes(routes, '/projects')
    expect(await screen.findByRole('heading', { name: 'Projects' })).toBeInTheDocument()
  })
})
