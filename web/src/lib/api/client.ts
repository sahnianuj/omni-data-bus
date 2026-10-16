import createClient, { type Middleware } from 'openapi-fetch'
import type { components, paths } from './schema'

export type Problem = components['schemas']['Problem']
export type FieldError = components['schemas']['FieldError']

/** An API failure carrying the server's problem+json details. */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly title: string
  readonly detail?: string
  readonly fieldErrors: FieldError[]
  readonly requestId?: string

  constructor(status: number, problem: Partial<Problem>, requestId?: string) {
    super(problem.detail || problem.title || `Request failed with status ${status}`)
    this.name = 'ApiError'
    this.status = status
    this.code = problem.code ?? 'INTERNAL'
    this.title = problem.title ?? 'Request failed'
    this.detail = problem.detail
    this.fieldErrors = problem.fieldErrors ?? []
    this.requestId = requestId ?? problem.instance
  }
}

let csrfToken: string | null = null
/** In-memory CSRF token store (the token comes from /auth/me and login responses). */
export const csrf = {
  get: () => csrfToken,
  set: (token: string | null) => {
    csrfToken = token
  },
}

type UnauthorizedHandler = (error: ApiError) => void
let unauthorizedHandler: UnauthorizedHandler | null = null
/** Registers the global 401 handler (redirect to sign-in). */
export function onUnauthorized(handler: UnauthorizedHandler | null) {
  unauthorizedHandler = handler
}

const MUTATING = new Set(['POST', 'PUT', 'PATCH', 'DELETE'])

// Untouched copies of mutating requests, kept so one can be replayed after a CSRF refresh.
const replayable = new WeakMap<Request, Request>()

const csrfMiddleware: Middleware = {
  onRequest({ request }) {
    if (!MUTATING.has(request.method.toUpperCase())) return request
    const token = csrf.get()
    if (token) request.headers.set('X-CSRF-Token', token)
    replayable.set(request, request.clone())
    return request
  },
  // A missing or stale token (e.g. the page loaded while the API was down, or the session was
  // replaced in another tab) is recovered by re-reading it from /auth/me and retrying once.
  async onResponse({ request, response }) {
    const copy = replayable.get(request)
    replayable.delete(request)
    if (!copy || response.status !== 403 || !(await isCsrfFailure(response))) return response
    const me = await globalThis.fetch(new URL('/api/v1/auth/me', request.url), { credentials: 'include' })
    csrf.set(me.ok ? ((await me.json()) as { csrfToken?: string }).csrfToken ?? null : null)
    const token = csrf.get()
    if (token) copy.headers.set('X-CSRF-Token', token)
    else copy.headers.delete('X-CSRF-Token')
    return globalThis.fetch(copy)
  },
}

async function isCsrfFailure(response: Response): Promise<boolean> {
  try {
    const body = (await response.clone().json()) as { code?: string }
    return body.code === 'CSRF_FAILED'
  } catch {
    return false
  }
}

export const api = createClient<paths>({
  baseUrl: typeof window === 'undefined' ? 'http://localhost' : window.location.origin,
  credentials: 'include',
  // Resolve fetch per request so test interceptors (MSW) and polyfills apply.
  fetch: (request: Request) => globalThis.fetch(request),
})
api.use(csrfMiddleware)

type FetchResult<T> = { data?: T; error?: unknown; response: Response }

/** Awaits an openapi-fetch call and returns its data, or throws ApiError. */
export async function call<T>(request: Promise<FetchResult<T>>): Promise<T> {
  const { data, error, response } = await request
  if (response.ok) return data as T
  const problem: Partial<Problem> = isProblem(error) ? error : { title: response.statusText }
  const err = new ApiError(response.status, problem, response.headers.get('X-Request-ID') ?? undefined)
  // A wrong password while signed in (re-confirming it before an export, say) is also a 401,
  // but the session is fine: only a lost session signs the user out.
  if (response.status === 401 && err.code !== 'INVALID_CREDENTIALS') unauthorizedHandler?.(err)
  throw err
}

function isProblem(value: unknown): value is Partial<Problem> {
  return typeof value === 'object' && value !== null && ('code' in value || 'title' in value)
}
