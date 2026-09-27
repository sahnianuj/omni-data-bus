import { describe, expect, it } from 'vitest'
import { scol } from '@/test/mapping'
import { generateFromTable, sampleDocument } from './generate'

const cols = [
  scol('customer_id', 'integer', { nullable: false }),
  scol('first_name', 'string'),
  scol('billing_street', 'string'),
  scol('billing_city', 'string'),
  scol('created_at', 'datetime', { udtName: 'timestamptz', nullable: false }),
  scol('tags', 'array', { elementType: 'string' }),
  scol('ref', 'string', { udtName: 'uuid' }),
]

describe('generateFromTable (mirrors docschema.FromTable)', () => {
  it('camelCase, nest by prefix and preserve', () => {
    const s = generateFromTable(cols, { naming: 'camelCase', nestByPrefix: true })
    expect(s.root.children!.map((f) => f.name)).toEqual(['customerId', 'firstName', 'billing', 'createdAt', 'tags', 'ref'])
    expect(s.root.children![0]).toMatchObject({ type: 'integer', required: true })
    expect(s.root.children![2]!.children!.map((f) => f.name)).toEqual(['street', 'city'])
    expect(s.root.children![3]).toMatchObject({ type: 'datetime', format: 'iso8601' })
    expect(s.root.children![4]!.items!.type).toBe('string')
    expect(s.root.children![5]!.format).toBe('uuid')
    const p = generateFromTable(cols, { naming: 'preserve', nestByPrefix: false })
    expect(p.root.children!.map((f) => f.name)).toEqual(cols.map((c) => c.name))
  })

  it('sample document for the JSON view', () => {
    const s = generateFromTable(cols.slice(0, 2), { naming: 'camelCase', nestByPrefix: false })
    expect(sampleDocument(s, { enabled: true, name: 'type', value: 'customer' })).toEqual({ type: 'customer', customerId: 0, firstName: 'string' })
  })
})
