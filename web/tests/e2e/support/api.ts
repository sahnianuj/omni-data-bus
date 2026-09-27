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

  private headers() {
    return { 'X-CSRF-Token': this.csrf }
  }

  async get<T = unknown>(path: string): Promise<T> {
    const res = await this.ctx.get(path)
    expect(res.ok(), `${path}: ${res.status()} ${await res.text()}`).toBeTruthy()
    return (await res.json()) as T
  }

  async send<T = unknown>(method: 'POST' | 'PUT' | 'PATCH' | 'DELETE', path: string, data?: unknown, ok = true): Promise<T> {
    const res = await this.ctx.fetch(path, { method, data, headers: this.headers() })
    if (ok) expect(res.ok(), `${method} ${path}: ${res.status()} ${await res.text()}`).toBeTruthy()
    const text = await res.text()
    return (text ? JSON.parse(text) : undefined) as T
  }

  /** A project with a saved and tested source and target (credentials stored once). */
  async projectWithRepos(name = `E2E ${uid()}`): Promise<{ id: string; sourceId: string; targetId: string }> {
    const p = await this.send<{ id: string }>('POST', '/api/v1/projects', { name })
    await this.send('PUT', `/api/v1/projects/${p.id}/connections/source`, {
      type: 'postgres',
      config: { host: env.source.host, port: env.source.port, database: env.source.database, username: env.source.username, sslMode: 'disable', connectTimeoutSec: 10 },
      secrets: { password: env.source.password },
    })
    await this.send('PUT', `/api/v1/projects/${p.id}/connections/target`, {
      type: 'couchbase',
      config: { connectionString: env.target.connectionString, username: env.target.username, deployment: 'self-managed', kvTimeoutMs: 10000, connectTimeoutMs: 30000 },
      secrets: { password: env.target.password },
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

  async dispose() {
    await this.ctx.dispose()
  }
}
