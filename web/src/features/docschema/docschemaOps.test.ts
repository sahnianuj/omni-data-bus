import { describe, expect, it } from 'vitest'
import type { DocSchema } from '@/features/mapping/types'
import { addField, duplicate, findField, leaves, move, OpError, pathsById, remove, rename, setDescription, setRequired, setType, unwrap, validate, walk, wrap } from './docschemaOps'

const schema = (): DocSchema => ({
  source: 'generated',
  root: {
    id: 'fld_root', name: '$', type: 'object',
    children: [
      { id: 'a', name: 'customerId', type: 'integer', required: true },
      { id: 'b', name: 'name', type: 'object', children: [{ id: 'b1', name: 'first', type: 'string' }, { id: 'b2', name: 'last', type: 'string' }] },
      { id: 'c', name: 'email', type: 'string' },
      { id: 'd', name: 'tags', type: 'array', items: { id: 'd1', name: '[]', type: 'string' } },
    ],
  },
})

describe('docschemaOps', () => {
  it('AC5: rename rejects duplicate sibling', () => {
    expect(() => rename(schema(), 'c', 'customerId')).toThrow(OpError)
    expect(() => rename(schema(), 'c', 'a.b')).toThrow("can't contain")
    expect(() => rename(schema(), 'c', '')).toThrow('1–128')
    expect(rename(schema(), 'b1', 'email').root.children![1]!.children![0]!.name, 'same name at another level is fine').toBe('email')
  })

  it('AC6: add/rename/setType', () => {
    let s = schema()
    const added = addField(s, 'fld_root', 'email', 'string')
    s = added.schema
    expect(findField(s, added.id)?.field.name, 'unique name').toBe('email2')
    expect(s.source).toBe('manual')
    s = rename(s, added.id, 'contactEmail')
    s = setType(s, added.id, 'object')
    expect(findField(s, added.id)?.field.children).toEqual([])
    s = setType(s, added.id, 'array')
    expect(findField(s, added.id)?.field.items?.type).toBe('string')
    s = setType(s, 'c', 'datetime')
    expect(findField(s, 'c')?.field.format).toBe('iso8601')
    expect(setType(s, 'c', 'datetime')).toBe(s)
    s = setRequired(s, 'c', true)
    expect(findField(s, 'c')?.field.required).toBe(true)
    s = setDescription(s, 'c', 'When')
    expect(findField(s, 'c')?.field.description).toBe('When')
    const nested = addField(s, 'b', 'middle', 'string', 1)
    expect(nested.schema.root.children![1]!.children!.map((f) => f.name)).toEqual(['first', 'middle', 'last'])
    expect(() => addField(s, 'c', 'x', 'string')).toThrow('only be added to objects')
  })

  it('AC6: unchanged subtrees keep their identity', () => {
    const s = schema()
    const next = rename(s, 'b1', 'given')
    expect(next.root.children![0]).toBe(s.root.children![0])
    expect(next.root.children![2]).toBe(s.root.children![2])
    expect(next.root.children![1]).not.toBe(s.root.children![1])
  })

  it('AC6: move preserves ids and updates paths', () => {
    const s = move(schema(), 'c', 'b', 0)
    expect(findField(s, 'c')?.path).toBe('name.email')
    expect(pathsById(s).get('c')).toBe('name.email')
    expect(s.root.children!.map((f) => f.id)).toEqual(['a', 'b', 'd'])
    const reordered = move(schema(), 'a', 'fld_root', 3)
    expect(reordered.root.children!.map((f) => f.id)).toEqual(['b', 'c', 'a', 'd'])
    expect(() => move(schema(), 'b', 'b1', 0)).toThrow()
    expect(() => move(schema(), 'b', 'b', 0)).toThrow('into itself')
    const clash = addField(schema(), 'b', 'email', 'string').schema
    expect(() => move(clash, 'c', 'b', 0)).toThrow('already has')
  })

  it('AC6: wrap and unwrap are inverses', () => {
    const wrapped = wrap(schema(), ['a', 'c'], 'contact')
    expect(wrapped.schema.root.children!.map((f) => f.name)).toEqual(['contact', 'name', 'tags'])
    expect(findField(wrapped.schema, 'c')?.path).toBe('contact.email')
    const back = unwrap(wrapped.schema, wrapped.id)
    expect(back.root.children!.map((f) => f.id)).toEqual(['a', 'c', 'b', 'd'])
    expect(() => wrap(schema(), ['a', 'b1'], 'x')).toThrow('share a parent')
    expect(() => unwrap(schema(), 'a')).toThrow()
  })

  it('duplicate, remove, leaves and validate', () => {
    const dup = duplicate(schema(), 'b')
    const copy = findField(dup.schema, dup.id)!
    expect(copy.field.name).toBe('nameCopy')
    expect(copy.field.children![0]!.id).not.toBe('b1')
    const removed = remove(schema(), 'b')
    expect(walk(removed).map((r) => r.path)).toEqual(['customerId', 'email', 'tags', 'tags[]'])
    expect(() => remove(schema(), 'fld_root')).toThrow()
    expect(leaves(schema()).map((l) => l.path)).toEqual(['customerId', 'name.first', 'name.last', 'email', 'tags'])
    expect(leaves(schema())[4]!.itemType).toBe('string')
    const bad: DocSchema = { source: 'manual', root: { id: 'r', name: '$', type: 'object', children: [{ id: 'x', name: 'a', type: 'string' }, { id: 'y', name: 'a', type: 'array' }] } }
    expect(validate(bad).map((i) => i.message)).toEqual(["Another field at this level is already called 'a'.", 'An array needs an item type.'])
    expect(validate(schema())).toEqual([])
  })
})
