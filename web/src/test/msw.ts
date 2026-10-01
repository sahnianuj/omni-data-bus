import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { afterAll, afterEach, beforeAll } from 'vitest'
import type { VersionInfo } from '@/features/system/buildInfo'
import type { TargetKind } from '@/features/targets/kinds'

export const sampleVersion = (over: Partial<VersionInfo> = {}): VersionInfo => ({
  version: '2.0.0',
  commit: '3f2a9c1e0b',
  buildTime: '2026-11-02T14:05:11Z',
  channel: 'release',
  goVersion: 'go1.26.8',
  platform: 'linux/amd64',
  dirty: false,
  metadataSchemaVersion: 7,
  nodes: { api: ['2.0.0'], worker: ['2.0.0'] },
  consistent: true,
  ...over,
})

export const targetKinds = (over: { directoryEnabled?: boolean } = {}): TargetKind[] => [
  {
    type: 'couchbase',
    role: 'target',
    displayName: 'Couchbase',
    icon: 'couchbase',
    capabilities: { createKeyspaces: true },
    enabled: true,
    kindCapabilities: {
      supportsDurability: true,
      supportsExpiry: true,
      supportsCreateLocation: true,
      requiresCredentials: true,
      maxDocBytes: 20971520,
      locationLevels: ['bucket', 'scope', 'collection'],
    },
  },
  {
    type: 'directory',
    role: 'target',
    displayName: 'Directory of JSON files',
    icon: 'folder',
    capabilities: {},
    enabled: over.directoryEnabled ?? true,
    disabledReason: over.directoryEnabled === false ? 'This server has no folder it can write to (no home folder or /data).' : undefined,
    kindCapabilities: {
      supportsDurability: false,
      supportsExpiry: false,
      supportsCreateLocation: true,
      requiresCredentials: false,
      maxDocBytes: 20971520,
      locationLevels: ['folder'],
    },
  },
]

/**
 * Handlers every page may need: the build footer (OC-058), the target kinds (OC-036), and an
 * empty relationship graph and virtual relationship list (the workbench's Related tables,
 * OC-042). resetHandlers keeps them.
 */
const defaultHandlers = [
  http.get('*/api/v1/version', () => HttpResponse.json(sampleVersion())),
  http.get('*/api/v1/target-kinds', () => HttpResponse.json({ kinds: targetKinds() })),
  http.get('*/api/v1/connections/:id/relations', ({ request }) => {
    const [schema = '', name = ''] = (new URL(request.url).searchParams.get('root') ?? '').split('.')
    return HttpResponse.json({ root: { schema, name }, depth: 4, paths: [], junctions: [], advice: [] })
  }),
  http.get('*/api/v1/projects/:id/relationships', () => HttpResponse.json({ items: [] })),
]

/** Shared MSW server; tests add handlers with server.use(...). resetHandlers keeps the defaults. */
export const server = setupServer(...defaultHandlers)

export function setupMswServer() {
  beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
  afterEach(() => server.resetHandlers())
  afterAll(() => server.close())
}
