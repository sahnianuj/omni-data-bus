import { describe, expect, it } from 'vitest'
import { directionOf, groupByDirection } from './relatedGroups'

describe('relatedGroups', () => {
  it('OC-157 AC1: splits rows by direction, keeping their order', () => {
    const row = (name: string, direction: 'references' | 'referencedBy') => ({ name, hop: { direction } })
    const { references, referencedBy } = groupByDirection([row('partners', 'references'), row('cart_items', 'referencedBy'), row('stores', 'references'), row('applied_rewards', 'referencedBy')])
    expect(references.map((r) => r.name)).toEqual(['partners', 'stores'])
    expect(referencedBy.map((r) => r.name)).toEqual(['cart_items', 'applied_rewards'])
  })

  it('OC-157 AC2: uses the server direction and cardinality, else derives them from the kind', () => {
    expect(directionOf({ kind: 'many-to-one', direction: 'referencedBy', cardinality: 'one-to-one' })).toEqual({ direction: 'referencedBy', cardinality: 'one-to-one' })
    expect(directionOf({ kind: 'many-to-one' })).toEqual({ direction: 'references', cardinality: 'many-to-one' })
    expect(directionOf({ kind: 'one-to-many' })).toEqual({ direction: 'referencedBy', cardinality: 'one-to-many' })
  })
})
