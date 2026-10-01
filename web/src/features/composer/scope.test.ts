import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import type { MappingSpec } from '@/features/mapping/types'
import { expressionNames, fieldScopes, inScope, scope } from './scope'

// The same fixture drives backend/internal/mapping/scope_test.go.
const fixture = JSON.parse(readFileSync(resolve(process.cwd(), '../backend/internal/mapping/testdata/scope-cases.json'), 'utf8')) as {
  spec: MappingSpec
  scopes: { fieldId: string; scope: string[] }[]
  sources: { fieldId: string; relation: string; column: string; inScope: boolean }[]
}

describe('composer scope', () => {
  it('AC2: matches server fixtures', () => {
    for (const c of fixture.scopes) expect(scope(fixture.spec, c.fieldId)?.map((e) => e.relationId), c.fieldId).toEqual(c.scope)
    const scopes = fieldScopes(fixture.spec)
    for (const c of fixture.sources) {
      expect(inScope(fixture.spec, scopes.get(c.fieldId)!, c.relation), `${c.fieldId} ← ${c.relation}.${c.column}`).toBe(c.inScope)
    }
  })

  it('carries each entry’s table, lookups and junctions', () => {
    const got = scope(fixture.spec, 'f_label')!
    expect(got[0]!.table).toEqual({ schema: 'public', name: 'orders' })
    expect(got[1]).toMatchObject({ relationId: 'rel_cust', lookup: true })
    expect(got[2]!.via).toEqual({ schema: 'public', name: 'order_tags' })
    expect(scope(fixture.spec, 'nope')).toBeUndefined()
  })

  it('lists the names an expression can use, without the row’s own columns', () => {
    expect(expressionNames(fixture.spec, 'f_sname', ['supplier_id', 'name'])).toEqual([
      '$root', '$parent', 'customers', 'items', 'order_items', 'products', 'rel_cust', 'rel_items', 'rel_prod', 'rel_supplier', 'supplier', 'suppliers',
    ])
  })
})
