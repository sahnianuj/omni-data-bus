import { readdirSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import type { MappingSpec } from './types'
import { upgradeSpec, upgradeView } from './upgradeSpec'

// The server's fixtures (backend/internal/mapping/upgrade_test.go runs the same files).
const dir = resolve(process.cwd(), '../backend/internal/mapping/testdata/specs')
const files = readdirSync(`${dir}/v1`).filter((f) => f.endsWith('.json'))
const read = (v: 'v1' | 'v2', f: string) => JSON.parse(readFileSync(`${dir}/${v}/${f}`, 'utf8')) as Record<string, unknown>

/** Drops what Go's encoder and the client write differently but mean the same: empty strings, false, null, [] and {}. */
function compact(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(compact)
  if (v && typeof v === 'object') {
    const out: Record<string, unknown> = {}
    for (const [k, x] of Object.entries(v)) {
      const c = compact(x)
      const empty = c === '' || c === false || c === null || c === undefined || (Array.isArray(c) && c.length === 0) || (typeof c === 'object' && c !== null && !Array.isArray(c) && Object.keys(c).length === 0)
      if (!empty) out[k] = c
    }
    return out
  }
  return v
}

describe('upgradeSpec', () => {
  it('AC5: matches server fixtures', () => {
    expect(files.length).toBeGreaterThanOrEqual(4)
    for (const f of files) {
      const got = upgradeSpec(read('v1', f) as MappingSpec)
      const want = read('v2', f) as unknown as MappingSpec
      // What the upgrade does, exactly as the server does it.
      expect(got.specVersion, f).toBe(2)
      expect(got.mode, f).toBe(want.mode)
      expect(got.aggregates, f).toEqual(want.aggregates)
      expect(got.relations.map((r) => r.parentId), f).toEqual(want.relations.map(() => null))
      expect(compact(got.relations), f).toEqual(compact(want.relations))
      expect(got.fieldMappings.map((m) => m.sources), f).toEqual(want.fieldMappings.map((m) => m.sources))
      expect(JSON.stringify(got), f).not.toContain('"embed')
      // Everything else is carried over unchanged.
      expect(compact(got), f).toEqual(compact(want))
    }
  })

  it('AC5: idempotent, and v2 specs pass through', () => {
    for (const f of files) {
      const once = upgradeSpec(read('v1', f) as MappingSpec)
      expect(upgradeSpec(once), f).toEqual(once)
      const v2 = read('v2', f) as unknown as MappingSpec
      expect(compact(upgradeSpec(v2)), f).toEqual(compact(v2))
    }
    const nested = { ...upgradeSpec({}), relations: [{ id: 'b', parentId: 'a', kind: 'many-to-one' as const, table: { schema: 's', name: 't' } }] }
    expect(upgradeSpec(nested).relations[0]!.parentId, 'a v2 tree is kept').toBe('a')
  })

  it('AC5: upgrades a cached view', () => {
    const view = { id: 'm1', spec: read('v1', 'embed_options.json') as unknown as MappingSpec }
    const up = upgradeView(view)
    expect(up.id).toBe('m1')
    expect(up.spec.relations.map((r) => r.id)).toEqual(['emb_items', 'emb_cust'])
    expect(up.spec.fieldMappings.find((m) => m.id === 'm_sku')!.sources).toEqual([{ relation: 'emb_items', column: 'sku' }])
  })
})
