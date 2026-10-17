import { describe, expect, it } from 'vitest'
import type { MappingSpec, RelationHop } from '@/features/mapping/types'
import { availableHops, documentSources, flatten } from './documentSources'

const T = (name: string) => ({ schema: 'sales', name })
const hop = (from: string, to: string, kind: 'many-to-one' | 'one-to-many', join: [string, string]): RelationHop => ({
  from: T(from), to: T(to), kind, join: [{ parent: join[0], child: join[1] }], indexed: true,
  direction: kind === 'many-to-one' ? 'references' : 'referencedBy', cardinality: kind,
})

// orders → items[] (order_items) → product (products); customer (customers) with billing and
// shipping addresses from the same table.
const spec = {
  specVersion: 2, mode: 'composed', key: { template: '' }, typeField: { enabled: false, name: '', value: '' }, nullHandling: 'include', fieldMappings: [],
  root: { table: T('orders') },
  docSchema: {
    source: 'generated',
    root: {
      id: 'fld_root', name: '$', type: 'object',
      children: [
        { id: 'f_items', name: 'items', type: 'array', items: { id: 'f_item', name: '[]', type: 'object', children: [{ id: 'f_product', name: 'product', type: 'object', children: [] }] } },
        { id: 'f_bill', name: 'billing', type: 'object', children: [] },
        { id: 'f_ship', name: 'shipping', type: 'object', children: [] },
      ],
    },
  },
  relations: [
    { id: 'r_i', parentId: null, kind: 'one-to-many', table: T('order_items'), join: [{ parent: 'order_id', child: 'order_id' }], targetFieldId: 'f_items' },
    { id: 'r_p', parentId: 'r_i', kind: 'many-to-one', table: T('products'), join: [{ parent: 'product_id', child: 'product_id' }], targetFieldId: 'f_product' },
    { id: 'r_b', parentId: null, kind: 'many-to-one', table: T('addresses'), join: [{ parent: 'bill_id', child: 'address_id' }], targetFieldId: 'f_bill' },
    { id: 'r_s', parentId: null, kind: 'many-to-one', table: T('addresses'), join: [{ parent: 'ship_id', child: 'address_id' }], targetFieldId: 'f_ship' },
  ],
} as unknown as MappingSpec

const hops: Record<string, RelationHop[]> = {
  products: [hop('products', 'brands', 'many-to-one', ['brand_id', 'brand_id']), hop('products', 'reviews', 'one-to-many', ['product_id', 'product_id']), hop('products', 'order_items', 'one-to-many', ['product_id', 'product_id'])],
  order_items: [hop('order_items', 'orders', 'many-to-one', ['order_id', 'order_id']), hop('order_items', 'products', 'many-to-one', ['product_id', 'product_id'])],
}
const hopsOf = (t: { name: string }) => hops[t.name] ?? []

describe('documentSources', () => {
  it('OC-142 AC1: instances nested like the document, and their available hops', () => {
    const root = documentSources(spec, T('orders'))
    expect(flatten(root).map((i) => [i.table.name, i.path, i.containerId, i.depth])).toEqual([
      ['orders', '', 'fld_root', 0],
      ['order_items', 'items[]', 'f_item', 1],
      ['products', 'items[].product', 'f_product', 2],
      ['addresses', 'billing', 'f_bill', 1],
      ['addresses', 'shipping', 'f_ship', 1],
    ])
    const product = flatten(root)[2]!
    const avail = availableHops(spec, product, hopsOf, () => [])!
    expect(avail.references.map((h) => h.to.name)).toEqual(['brands'])
    // reviews, but not the way back to order_items (it embedded the product).
    expect(avail.referencedBy.map((h) => ('to' in h ? h.to.name : h.table.name))).toEqual(['reviews'])
    const items = flatten(root)[1]!
    // order_items: products is embedded under it, orders is the way back.
    expect(availableHops(spec, items, hopsOf, () => [])!.references).toEqual([])
    expect(availableHops(spec, items, () => undefined, () => [])).toBeUndefined() // still loading
  })

  it('OC-142 AC1: the same table embedded twice is two instances', () => {
    const addresses = flatten(documentSources(spec, T('orders'))).filter((i) => i.table.name === 'addresses')
    expect(addresses.map((i) => [i.key, i.path])).toEqual([['r_b', 'billing'], ['r_s', 'shipping']])
  })
})
