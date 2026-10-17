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
 * Handlers every page may need: the build footer (OC-058), the target kinds (OC-036), an empty
 * relationship graph and virtual relationship list (the workbench's Related tables, OC-042), and
 * table hops derived from the graph (OC-141). resetHandlers keeps them.
 */
const defaultHandlers = [
  http.get('*/api/v1/version', () => HttpResponse.json(sampleVersion())),
  http.get('*/api/v1/target-kinds', () => HttpResponse.json({ kinds: targetKinds() })),
  http.get('*/api/v1/connections/:id/relations', ({ request }) => {
    const [schema = '', name = ''] = (new URL(request.url).searchParams.get('root') ?? '').split('.')
    return HttpResponse.json({ root: { schema, name }, depth: 4, paths: [], junctions: [], advice: [] })
  }),
  // A table's hops (OC-141) follow whatever graph the test serves: every hop leaving the table.
  http.get('*/api/v1/connections/:id/relations/hops', async ({ request, params }) => {
    const table = new URL(request.url).searchParams.get('table') ?? ''
    const [schema = '', name = ''] = table.split('.')
    const res = await fetch(new URL(`/api/v1/connections/${String(params.id)}/relations?root=${encodeURIComponent(table)}&depth=4`, request.url))
    const graph = (await res.json()) as { paths: { hops: { from: { schema: string; name: string }; to: { schema: string; name: string }; kind: string; join: { parent: string; child: string }[] }[] }[]; junctions: unknown[]; advice: unknown[] }
    const seen = new Set<string>()
    const hops = graph.paths.flatMap((p) => p.hops).filter((h) => {
      const k = `${h.to.schema}.${h.to.name}|${h.kind}|${JSON.stringify(h.join)}`
      if (h.from.schema !== schema || h.from.name !== name || seen.has(k)) return false
      seen.add(k)
      return true
    })
    return HttpResponse.json({ table: { schema, name }, hops, junctions: graph.junctions ?? [], advice: graph.advice ?? [], maxRelationDepth: 4 })
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
