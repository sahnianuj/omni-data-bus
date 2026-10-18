import { describe, expect, it } from 'vitest'
import { embedView, sampleSpec } from '@/test/mapping'
import { buildEdges } from './buildEdges'
import { mappingReads } from './reads'
import { sourceStates } from './selectVisible'
import type { FieldMapping, MappingSpec } from './types'

const expr = (id: string, targetFieldId: string, e: string): FieldMapping => ({ id, targetFieldId, sources: [], transforms: [{ op: 'expression', args: { expr: e } }], origin: 'manual', state: 'active' })

describe('mappingReads: columns an expression reads count as used', () => {
  it('row.x, bare columns, $root, relation names; not strings or helpers', () => {
    const spec = embedView().spec as MappingSpec
    const known = new Set(['first_name', 'last_name', 'customer_id', 'emb_o:total', 'emb_o:order_date'])
    const reads = mappingReads(spec, known)
    expect(reads(expr('m', 'f_first', "concat(row.first_name, ' ', row.last_name)"))).toEqual(['first_name', 'last_name'])
    expect(reads(expr('m', 'f_first', 'upper(first_name)'))).toEqual(['first_name'])
    // Inside the orders[] embed: row is the order; $root and the relation's own names reach out.
    expect(reads(expr('m', 'f_total', 'row.total + $root.customer_id'))).toEqual(['emb_o:total', 'customer_id'])
    expect(reads(expr('m', 'f_first', 'orders.order_date'))).toEqual(['emb_o:order_date'])
    expect(reads(expr('m', 'f_first', "'row.last_name' + \"first_name\""))).toEqual([])
  })

  it('the dot is green and the line drawn for a column only an expression reads', () => {
    const spec = sampleSpec()
    spec.fieldMappings = spec.fieldMappings.filter((m) => m.id !== 'm_first')
    spec.fieldMappings.push(expr('m_name', 'f_first', "concat(row.first_name, ' ', row.last_name)"))
    const cols = ['first_name', 'last_name', 'age'].map((name) => ({ name }) as never)
    const ss = sourceStates(spec, cols)
    expect([ss.get('first_name'), ss.get('last_name'), ss.get('age')]).toEqual(['manual', 'manual', 'unmapped'])
    const edges = buildEdges(spec, new Set(), new Map(), [], mappingReads(spec, new Set(['first_name', 'last_name'])))
    expect(edges.filter((e) => e.mappingId === 'm_name').map((e) => [e.from, e.multi])).toEqual([['first_name', true], ['last_name', true]])
  })
})
