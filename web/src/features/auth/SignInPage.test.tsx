import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { server, setupMswServer } from '@/test/msw'
import { renderRoutes } from '@/test/render'
import { mockSession, sampleSession } from '@/test/session'
import { safeNext } from './api'

setupMswServer()
beforeEach(() => mockSession(null))

async function signIn(email = 'ada@example.com', password = 'correct horse battery staple') {
  await userEvent.type(await screen.findByLabelText('Email'), email)
  await userEvent.type(screen.getByLabelText('Password'), password)
  await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))
}

function loginOK() {
  server.use(
    http.post('*/api/v1/auth/login', () => {
      mockSession('owner')
      return HttpResponse.json(sampleSession())
    }),
  )
}

describe('SignInPage', () => {
  it('AC8: navigates to next after login', async () => {
    loginOK()
    const { router } = renderRoutes(routes, '/signin?next=%2Fprojects')
    await signIn()
    await waitFor(() => expect(router.state.location.pathname).toBe('/projects'))
  })

  it('AC8: ignores an off-site next (no open redirect)', async () => {
    loginOK()
    const { router } = renderRoutes(routes, '/signin?next=https%3A%2F%2Fevil.example')
    await signIn()
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
  })

  it('AC3: shows a generic error for bad credentials', async () => {
    server.use(
      http.post('*/api/v1/auth/login', () =>
        HttpResponse.json({ title: 'Sign-in failed', status: 401, code: 'INVALID_CREDENTIALS', detail: 'The email or password is incorrect.' }, { status: 401 }),
      ),
    )
    renderRoutes(routes, '/signin')
    await signIn()
    expect(await screen.findByRole('alert')).toHaveTextContent('The email or password is incorrect.')
  })

  it('AC7: shows retry-after message on 429', async () => {
    server.use(
      http.post('*/api/v1/auth/login', () =>
        HttpResponse.json(
          { title: 'Too many attempts', status: 429, code: 'RATE_LIMITED', detail: 'Too many sign-in attempts. Try again in 42 seconds.' },
          { status: 429, headers: { 'Retry-After': '42' } },
        ),
      ),
    )
    renderRoutes(routes, '/signin')
    await signIn()
    expect(await screen.findByRole('alert')).toHaveTextContent('Too many attempts. Try again in 42 seconds.')
  })

  it('redirects away when already signed in', async () => {
    mockSession('owner')
    const { router } = renderRoutes(routes, '/signin')
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
  })
})

describe('safeNext', () => {
  it.each([
    [null, '/'],
    ['/projects?x=1', '/projects?x=1'],
    ['//evil.example', '/'],
    ['/\\evil.example', '/'],
    ['https://evil.example', '/'],
    ['projects', '/'],
  ])('%s → %s', (input, want) => expect(safeNext(input)).toBe(want))
})
