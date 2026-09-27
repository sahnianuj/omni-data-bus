import { describe, expect, it } from 'vitest'
import { shopSnapshot, tbl } from '@/test/discovery'
import { relationshipGraph } from './relationshipGraph'

const customer = shopSnapshot().schemas[0]!.tables.find((t) => t.name === 'customer')!

describe('relationshipGraph', () => {
  it('AC4: builds nodes/edges for parents and children', () => {
    const g = relationshipGraph('shop', customer)
    expect(g.self).toEqual({ id: 'shop.customer', schema: 'shop', table: 'customer', role: 'self' })
    expect(g.parents.map((n) => n.id)).toEqual(['shop.address'])
    expect(g.children.map((n) => n.id)).toEqual(['shop.orders'])
    expect(g.nodes).toHaveLength(3)
    expect(g.edges).toEqual([
      { from: 'shop.customer', to: 'shop.address', name: 'customer_address_fk', columns: ['address_id'], refColumns: ['address_id'] },
      { from: 'shop.orders', to: 'shop.customer', name: 'orders_customer_fk', columns: ['customer_id'], refColumns: ['customer_id'] },
    ])
  })

  it('dedupes neighbours with several FKs and keeps self-references as edges', () => {
    const employee = tbl('employee', {
      foreignKeys: [
        { name: 'manager_fk', columns: ['manager_id'], refSchema: 'hr', refTable: 'employee', refColumns: ['id'] },
        { name: 'home_fk', columns: ['home_id'], refSchema: 'hr', refTable: 'office', refColumns: ['id'] },
        { name: 'work_fk', columns: ['work_id'], refSchema: 'hr', refTable: 'office', refColumns: ['id'] },
      ],
      referencedBy: [{ schema: 'hr', table: 'employee', name: 'manager_fk', columns: ['manager_id'], refColumns: ['id'] }],
    })
    const g = relationshipGraph('hr', employee)
    expect(g.parents.map((n) => n.id)).toEqual(['hr.office'])
    expect(g.children).toEqual([])
    expect(g.edges.map((e) => e.name)).toEqual(['manager_fk', 'home_fk', 'work_fk'])
  })
})
