import { setupServer } from 'msw/node'
import { afterAll, afterEach, beforeAll } from 'vitest'

/** Shared MSW server; tests add handlers with server.use(...). */
export const server = setupServer()

export function setupMswServer() {
  beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
  afterEach(() => server.resetHandlers())
  afterAll(() => server.close())
}
