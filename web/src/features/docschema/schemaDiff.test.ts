import { describe, expect, it } from 'vitest'
import type { DocSchema } from '@/features/mapping/types'
import { schemaDiff } from './schemaDiff'

const s = (children: DocSchema['root']['children']): DocSchema => ({ source: 'manual', root: { id: 'r', name: '$', type: 'object', children } })

describe('schemaDiff', () => {
  it('AC7: added/removed/type-changed', () => {
    const a = s([{ id: '1', name: 'id', type: 'integer' }, { id: '2', name: 'name', type: 'string' }, { id: '3', name: 'age', type: 'string' }])
    const b = s([{ id: 'x', name: 'id', type: 'integer' }, { id: 'y', name: 'age', type: 'integer' }, { id: 'z', name: 'tags', type: 'array', items: { id: 'z1', name: '[]', type: 'string' } }])
    expect(schemaDiff(a, b)).toEqual({ added: ['tags', 'tags[]'], removed: ['name'], typeChanged: [{ path: 'age', from: 'string', to: 'integer' }] })
    expect(schemaDiff(a, a)).toEqual({ added: [], removed: [], typeChanged: [] })
  })
})
