import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, call, csrf } from '@/lib/api/client'
import type { components } from '@/lib/api/schema'

export type SessionInfo = components['schemas']['SessionResponse']
export type Role = SessionInfo['role']

export const sessionKey = ['auth', 'me'] as const

function remember(s: SessionInfo) {
  csrf.set(s.csrfToken)
  return s
}

/** The signed-in session, or null when signed out. */
export function useSession() {
  return useQuery({
    queryKey: sessionKey,
    staleTime: 5 * 60_000,
    retry: false,
    queryFn: async (): Promise<SessionInfo | null> => {
      try {
        return remember(await call(api.GET('/api/v1/auth/me')))
      } catch (e) {
        if (e instanceof ApiError && e.status === 401) {
          csrf.set(null)
          return null
        }
        throw e
      }
    },
  })
}

export function useSignIn() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: components['schemas']['LoginRequest']) => remember(await call(api.POST('/api/v1/auth/login', { body }))),
    onSuccess: (s) => qc.setQueryData(sessionKey, s),
  })
}

export function useSignUp() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: components['schemas']['SignupRequest']) => remember(await call(api.POST('/api/v1/auth/signup', { body }))),
    onSuccess: (s) => qc.setQueryData(sessionKey, s),
  })
}

export function useSignOut() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      await call(api.POST('/api/v1/auth/logout'))
    },
    onSettled: () => {
      csrf.set(null)
      qc.clear()
      qc.setQueryData(sessionKey, null)
    },
  })
}

/** Only same-origin relative paths are allowed as post-sign-in destinations (no open redirects). */
export function safeNext(next: string | null): string {
  if (!next || !next.startsWith('/') || next.startsWith('//') || next.startsWith('/\\')) return '/'
  return next
}
