import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { csrf } from '@/lib/api/client'
import { server, setupMswServer } from '@/test/msw'
import { renderRoutes } from '@/test/render'
import { mockSession, sampleSession } from '@/test/session'

setupMswServer()
beforeEach(() => mockSession(null))

async function fill(name: string, email: string, password: string) {
  await userEvent.type(await screen.findByLabelText('Your name'), name)
  await userEvent.type(screen.getByLabelText('Email'), email)
  await userEvent.type(document.getElementById('password')!, password)
}

describe('SignUpPage', () => {
  it('AC1: submits and redirects to dashboard', async () => {
    let body: unknown
    server.use(
      http.post('*/api/v1/auth/signup', async ({ request }) => {
        body = await request.json()
        mockSession('owner')
        return HttpResponse.json(sampleSession(), { status: 201 })
      }),
    )
    const { router } = renderRoutes(routes, '/signup')
    await fill('Ada Lovelace', 'ada@example.com', 'correct horse battery staple')
    expect(screen.getByText(/Password strength/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Create account' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
    expect(body).toEqual({ displayName: 'Ada Lovelace', email: 'ada@example.com', password: 'correct horse battery staple' })
    expect(csrf.get()).toBe('csrf-123')
  })

  it('AC1: validates on the client before calling the server', async () => {
    renderRoutes(routes, '/signup')
    await fill('A', 'not-an-email', 'short')
    await userEvent.click(screen.getByRole('button', { name: 'Create account' }))
    expect(await screen.findByText('Enter a valid email address')).toBeInTheDocument()
    expect(screen.getByText('Must be at least 12 characters')).toBeInTheDocument()
  })

  it('AC1: shows server field errors', async () => {
    server.use(
      http.post('*/api/v1/auth/signup', () =>
        HttpResponse.json(
          { title: 'Email already registered', status: 409, code: 'EMAIL_TAKEN', detail: 'exists', fieldErrors: [{ field: 'email', message: 'is already registered' }] },
          { status: 409 },
        ),
      ),
    )
    renderRoutes(routes, '/signup')
    await fill('Ada', 'ada@example.com', 'correct horse battery staple')
    await userEvent.click(screen.getByRole('button', { name: 'Create account' }))
    expect(await screen.findByText('Is already registered')).toBeInTheDocument()
  })
})
