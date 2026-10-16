import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { server, setupMswServer } from '@/test/msw'
import { api, ApiError, call, csrf, onUnauthorized } from './client'

setupMswServer()
afterEach(() => {
  csrf.set(null)
  onUnauthorized(null)
})

describe('api client', () => {
  it('returns data on success', async () => {
    server.use(http.get('*/api/v1/version', () => HttpResponse.json({ version: '1.0.0', commit: 'abc' })))
    await expect(call(api.GET('/api/v1/version'))).resolves.toEqual({ version: '1.0.0', commit: 'abc' })
  })

  it('AC8: parses problem+json into ApiError', async () => {
    server.use(
      http.get('*/api/v1/version', () =>
        HttpResponse.json(
          {
            type: 'x',
            title: 'Invalid request',
            status: 422,
            code: 'VALIDATION_FAILED',
            detail: 'One or more fields are invalid.',
            fieldErrors: [{ field: 'port', message: 'must be at least 1' }],
          },
          { status: 422, headers: { 'Content-Type': 'application/problem+json', 'X-Request-ID': 'req-9' } },
        ),
      ),
    )
    const err = await call(api.GET('/api/v1/version')).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    const apiErr = err as ApiError
    expect(apiErr.status).toBe(422)
    expect(apiErr.code).toBe('VALIDATION_FAILED')
    expect(apiErr.detail).toBe('One or more fields are invalid.')
    expect(apiErr.fieldErrors).toEqual([{ field: 'port', message: 'must be at least 1' }])
    expect(apiErr.requestId).toBe('req-9')
  })

  it('AC8: non-problem failures still become ApiError', async () => {
    server.use(http.get('*/api/v1/version', () => new HttpResponse('boom', { status: 502, statusText: 'Bad Gateway' })))
    const err = (await call(api.GET('/api/v1/version')).catch((e: unknown) => e)) as ApiError
    expect(err.status).toBe(502)
    expect(err.code).toBe('INTERNAL')
  })

  it('AC8: adds CSRF header on POST/PUT/PATCH/DELETE only', async () => {
    csrf.set('tok-123')
    const seen: Record<string, string | null> = {}
    server.use(
      http.all('*/echo', ({ request }) => {
        seen[request.method] = request.headers.get('X-CSRF-Token')
        return HttpResponse.json({})
      }),
    )
    const raw = api as unknown as Record<string, (path: string) => Promise<unknown>>
    for (const m of ['GET', 'POST', 'PUT', 'PATCH', 'DELETE']) await raw[m]!('/echo')
    expect(seen).toEqual({ GET: null, POST: 'tok-123', PUT: 'tok-123', PATCH: 'tok-123', DELETE: 'tok-123' })
  })

  it('AC8: 401 triggers the unauthorized handler', async () => {
    const handler = vi.fn()
    onUnauthorized(handler)
    server.use(
      http.get('*/api/v1/version', () =>
        HttpResponse.json({ title: 'Sign in required', code: 'UNAUTHENTICATED', status: 401 }, { status: 401 }),
      ),
    )
    await expect(call(api.GET('/api/v1/version'))).rejects.toBeInstanceOf(ApiError)
    expect(handler).toHaveBeenCalledOnce()
  })

  it("a wrong password while signed in doesn't sign out", async () => {
    const handler = vi.fn()
    onUnauthorized(handler)
    server.use(
      http.post('*/api/v1/auth/step-up', () =>
        HttpResponse.json({ title: 'Wrong password', code: 'INVALID_CREDENTIALS', status: 401 }, { status: 401 }),
      ),
    )
    await expect(call(api.POST('/api/v1/auth/step-up', { body: { password: 'nope' } }))).rejects.toMatchObject({ code: 'INVALID_CREDENTIALS' })
    expect(handler).not.toHaveBeenCalled()
  })

  it('AC5: refreshes a missing CSRF token from /auth/me and retries once', async () => {
    const sent: (string | null)[] = []
    server.use(
      http.get('*/api/v1/auth/me', () => HttpResponse.json({ csrfToken: 'fresh-tok' })),
      http.post('*/api/v1/projects', async ({ request }) => {
        const token = request.headers.get('X-CSRF-Token')
        sent.push(token)
        if (token !== 'fresh-tok') {
          return HttpResponse.json({ status: 403, code: 'CSRF_FAILED', title: 'Request blocked' }, { status: 403 })
        }
        return HttpResponse.json(await request.json(), { status: 201 })
      }),
    )
    const data = await call(api.POST('/api/v1/projects', { body: { name: 'p' } }))
    expect(data).toEqual({ name: 'p' })
    expect(sent).toEqual([null, 'fresh-tok'])
    expect(csrf.get()).toBe('fresh-tok')
  })

  it('AC5: other 403s are not retried', async () => {
    let calls = 0
    server.use(
      http.post('*/api/v1/projects', () => {
        calls++
        return HttpResponse.json({ status: 403, code: 'FORBIDDEN', title: 'No' }, { status: 403 })
      }),
    )
    const err = (await call(api.POST('/api/v1/projects', { body: { name: 'p' } })).catch((e: unknown) => e)) as ApiError
    expect(err.code).toBe('FORBIDDEN')
    expect(calls).toBe(1)
  })
})
