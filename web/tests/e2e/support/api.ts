import { expect, request, type APIRequestContext } from '@playwright/test'
import { env, password, uid } from './env'

export type Role = 'owner' | 'editor' | 'viewer'

/**
 * A signed-in API client for setting up data quickly (the specs drive the UI for what they test).
 * It sends the session's CSRF token on mutating requests, like the web app.
 */
export class Api {
  private constructor(
    readonly ctx: APIRequestContext,
    readonly email: string,
    private csrf: string,
    readonly userId: string,
    readonly workspaceId: string,
  ) {}

  static async signUp(name = 'E2E User'): Promise<Api> {
    const ctx = await request.newContext({ baseURL: env.baseURL, extraHTTPHeaders: { Origin: env.baseURL } })
    const email = `e2e-${uid()}-${Date.now()}@example.com`
    const res = await ctx.post('/api/v1/auth/signup', { data: { email, password, displayName: name } })
    expect(res.status(), await res.text()).toBe(201)
    const s = await res.json()
    return new Api(ctx, email, s.csrfToken, s.user.id, s.workspace.id)
  }

  /** Signs in an existing user (a new session in whatever workspace they now belong to). */
  static async signIn(email: string): Promise<Api> {
    const ctx = await request.newContext({ baseURL: env.baseURL, extraHTTPHeaders: { Origin: env.baseURL } })
    const res = await ctx.post('/api/v1/auth/login', { data: { email, password } })
    expect(res.status(), await res.text()).toBe(200)
    const s = await res.json()
    return new Api(ctx, email, s.csrfToken, s.user.id, s.workspace.id)
  }

  /** The raw status of a request (for access-control checks). */
  async status(method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE', path: string, data?: unknown): Promise<number> {
    const res = await this.ctx.fetch(path, { method, data, headers: this.headers() })
    return res.status()
  }

  private headers() {
    return { 'X-CSRF-Token': this.csrf }
  }

  async get<T = unknown>(path: string): Promise<T> {
    const res = await this.ctx.get(path)
    expect(res.ok(), `${path}: ${res.status()} ${await res.text()}`).toBeTruthy()
    return (await res.json()) as T
  }

  async getText(path: string): Promise<string> {
    const res = await this.ctx.get(path)
    expect(res.ok(), `${path}: ${res.status()}`).toBeTruthy()
    return res.text()
  }

  async sendWithHeaders<T = unknown>(method: 'POST' | 'PUT' | 'PATCH' | 'DELETE', path: string, data: unknown, headers: Record<string, string>): Promise<T> {
    const res = await this.ctx.fetch(path, { method, data, headers: { ...this.headers(), ...headers } })
    expect(res.ok(), `${method} ${path}: ${res.status()} ${await res.text()}`).toBeTruthy()
    const text = await res.text()
    return (text ? JSON.parse(text) : undefined) as T
  }

  async send<T = unknown>(method: 'POST' | 'PUT' | 'PATCH' | 'DELETE', path: string, data?: unknown, ok = true): Promise<T> {
    const res = await this.ctx.fetch(path, { method, data, headers: this.headers() })
    if (ok) expect(res.ok(), `${method} ${path}: ${res.status()} ${await res.text()}`).toBeTruthy()
    const text = await res.text()
    return (text ? JSON.parse(text) : undefined) as T
  }

  /** Saves and tests the project's PostgreSQL source (the sample database). */
  async sourceRepo(projectId: string) {
    await this.send('PUT', `/api/v1/projects/${projectId}/connections/source`, {
      type: 'postgres',
      config: { host: env.source.host, port: env.source.port, database: env.source.database, username: env.source.username, sslMode: 'disable', connectTimeoutSec: 10 },
      secrets: { password: env.source.password },
    })
    await this.send('POST', `/api/v1/connections/${encodeURIComponent(`${projectId}::source`)}/test`)
  }

  /** A project with a saved and tested source and target (credentials stored once). */
  async projectWithRepos(
    name = `E2E ${uid()}`,
    creds: { source?: { username: string; password: string }; target?: { username: string; password: string } } = {},
  ): Promise<{ id: string; sourceId: string; targetId: string }> {
    const src = creds.source ?? { username: env.source.username, password: env.source.password }
    const tgt = creds.target ?? { username: env.target.username, password: env.target.password }
    const p = await this.send<{ id: string }>('POST', '/api/v1/projects', { name })
    await this.send('PUT', `/api/v1/projects/${p.id}/connections/source`, {
      type: 'postgres',
      config: { host: env.source.host, port: env.source.port, database: env.source.database, username: src.username, sslMode: 'disable', connectTimeoutSec: 10 },
      secrets: { password: src.password },
    })
    await this.send('PUT', `/api/v1/projects/${p.id}/connections/target`, {
      type: 'couchbase',
      config: { connectionString: env.target.connectionString, username: tgt.username, deployment: 'self-managed', kvTimeoutMs: 10000, connectTimeoutMs: 30000 },
      secrets: { password: tgt.password },
    })
    const sourceId = `${p.id}::source`
    const targetId = `${p.id}::target`
    await this.send('POST', `/api/v1/connections/${encodeURIComponent(sourceId)}/test`)
    await this.send('POST', `/api/v1/connections/${encodeURIComponent(targetId)}/test`)
    await this.discover(sourceId)
    return { id: p.id, sourceId, targetId }
  }

  /** Discovers the source schema and waits for it to finish. */
  async discover(sourceId: string) {
    const id = encodeURIComponent(sourceId)
    const res = await this.send<{ status: string; id: string }>('POST', `/api/v1/connections/${id}/discover`, {})
    if (res.status === 'running') {
      await expect
        .poll(async () => (await this.get<{ status: string }>(`/api/v1/connections/${id}/discoveries/${res.id}`)).status, { timeout: 60_000 })
        .toBe('completed')
    }
  }

  /** Creates mappings for tables into sandbox.e2e_<random>.<table> (collections created at migration start). */
  async mappings(projectId: string, tables: string[], scope = `e2e_${uid()}`) {
    const res = await this.send<{ results: { mapping?: { id: string }; error?: unknown }[] }>('POST', `/api/v1/projects/${projectId}/mappings:bulk`, {
      items: tables.map((t) => {
        const [schema, table] = t.split('.')
        return { sourceSchema: schema, sourceTable: table, target: { bucket: env.target.bucket, scope, collection: table, createIfMissing: true } }
      }),
    })
    return { scope, ids: res.results.map((r) => r.mapping!.id) }
  }

  /** Starts a migration of every mapping of the project (the pre-flight must pass). */
  async startJob(projectId: string, options: Record<string, unknown> = {}): Promise<Job> {
    const res = await this.send<{ job: Job }>('POST', `/api/v1/projects/${projectId}/jobs`, options)
    return res.job
  }

  job(id: string) {
    return this.get<Job>(`/api/v1/jobs/${id}`)
  }

  /** Waits until the job reaches one of statuses and returns it. */
  async waitJob(id: string, statuses: string[], timeout = 120_000): Promise<Job> {
    await expect.poll(async () => (await this.job(id)).status, { timeout, intervals: [500, 1000] }).toMatch(new RegExp(`^(${statuses.join('|')})$`))
    return this.job(id)
  }

  /** Waits until the job has written at least n documents. */
  async waitWritten(id: string, n: number, timeout = 120_000) {
    await expect.poll(async () => (await this.job(id)).progress.written, { timeout, intervals: [250, 500] }).toBeGreaterThanOrEqual(n)
  }

  mapping(projectId: string, mappingId: string) {
    return this.get<MappingView>(`/api/v1/projects/${projectId}/mappings/${mappingId}`)
  }

  /** Changes a mapping's spec (read-modify-write with its version). */
  async editMapping(projectId: string, mappingId: string, edit: (spec: MappingView['spec']) => void) {
    const m = await this.mapping(projectId, mappingId)
    edit(m.spec)
    const res = await this.ctx.fetch(`/api/v1/projects/${projectId}/mappings/${mappingId}`, {
      method: 'PUT',
      data: { spec: m.spec },
      headers: { ...this.headers(), 'If-Match': `"${m.version}"` },
    })
    expect(res.ok(), `edit mapping: ${res.status()} ${await res.text()}`).toBeTruthy()
  }

  /** Sets the transform steps of the field mapping reading column. */
  async setTransforms(projectId: string, mappingId: string, column: string, transforms: { op: string; args?: Record<string, unknown> }[]) {
    await this.editMapping(projectId, mappingId, (spec) => {
      const fm = spec.fieldMappings.find((f) => f.sources.length === 1 && f.sources[0]!.column === column && !f.sources[0]!.embed)
      expect(fm, `a mapping of ${column}`).toBeTruthy()
      fm!.transforms = transforms
    })
  }

  async dispose() {
    await this.ctx.dispose()
  }
}

export type Job = {
  id: string
  status: string
  reason?: string
  retryable: boolean
  progress: { written: number; failed: number; skipped: number; percent: number; total: number }
}

export type MappingView = {
  id: string
  version: number
  status: 'ready' | 'draft' | 'stale' | string
  target: { bucket: string; scope: string; collection: string }
  spec: {
    key: { template: string }
    fieldMappings: { id: string; sources: { column: string; embed?: string }[]; transforms: { op: string; args?: Record<string, unknown> }[] }[]
  } & Record<string, unknown>
}
