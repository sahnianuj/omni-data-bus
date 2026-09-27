import { http, HttpResponse } from 'msw'
import type { SessionInfo } from '@/features/auth/api'
import { server } from './msw'

export const sampleSession = (role: SessionInfo['role'] = 'owner'): SessionInfo => ({
  user: { id: 'u1', email: 'ada@example.com', displayName: 'Ada Lovelace' },
  workspace: { id: 'w1', name: "Ada Lovelace's workspace" },
  role,
  csrfToken: 'csrf-123',
})

/** Makes GET /auth/me return a signed-in session (or 401 when role is null). */
export function mockSession(role: SessionInfo['role'] | null = 'owner') {
  server.use(
    http.get('*/api/v1/auth/me', () =>
      role
        ? HttpResponse.json(sampleSession(role))
        : HttpResponse.json({ title: 'Sign in required', code: 'UNAUTHENTICATED', status: 401 }, { status: 401 }),
    ),
  )
}
