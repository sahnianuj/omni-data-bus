import { describe, expect, it } from 'vitest'
import { sanitizeCollectionName } from './names'

describe('sanitizeCollectionName (mirrors the server)', () => {
  it('AC1: spaces, uppercase, leading _, length, unicode', () => {
    expect(sanitizeCollectionName('Order Details')).toBe('order_details')
    expect(sanitizeCollectionName('CustomerOrders')).toBe('customer_orders')
    expect(sanitizeCollectionName('_hidden')).toBe('hidden')
    expect(sanitizeCollectionName('%pct')).toBe('pct')
    expect(sanitizeCollectionName('ümlaut Größe')).toBe('mlaut_gr_e')
    expect(sanitizeCollectionName('a'.repeat(300))).toHaveLength(251)
    expect(sanitizeCollectionName('___')).toBe('collection')
    expect(sanitizeCollectionName('order-items')).toBe('order_items')
  })
})
