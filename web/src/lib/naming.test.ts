import { describe, expect, it } from 'vitest'
import { camel, rename, snake, words } from './naming'

describe('naming (mirrors backend/internal/naming)', () => {
  it('splits words', () => {
    expect(words('customerID')).toEqual(['customer', 'ID'])
    expect(words('CUSTOMER-ID')).toEqual(['CUSTOMER', 'ID'])
    expect(words('addr2')).toEqual(['addr', '2'])
    expect(words('URLValue')).toEqual(['URL', 'Value'])
    expect(words('')).toEqual([])
  })

  it('camel and snake', () => {
    expect(camel('order_ID2')).toBe('orderId2')
    expect(camel('URL')).toBe('url')
    expect(camel('loyalty_tier')).toBe('loyaltyTier')
    expect(snake('Order Details')).toBe('order_details')
    expect(rename('loyalty_tier', 'preserve')).toBe('loyalty_tier')
    expect(rename('loyaltyTier', 'snake_case')).toBe('loyalty_tier')
    expect(camel('')).toBe('')
  })
})
