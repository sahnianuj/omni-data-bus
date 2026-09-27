import { request } from '@playwright/test'

/** Waits for the stack: the API must report ready (metadata store reachable). */
export default async function globalSetup() {
  const baseURL = process.env.E2E_BASE_URL ?? 'http://localhost:18081'
  const ctx = await request.newContext({ baseURL })
  const deadline = Date.now() + 120_000
  for (;;) {
    try {
      const res = await ctx.get('/readyz')
      if (res.ok()) break
    } catch {
      // not listening yet
    }
    if (Date.now() > deadline) throw new Error(`${baseURL}/readyz never became ready; start the stack with scripts/e2e.sh`)
    await new Promise((r) => setTimeout(r, 1000))
  }
  await ctx.dispose()
}
