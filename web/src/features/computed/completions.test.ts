import { describe, expect, it } from 'vitest'
import type { MappingSpec } from '@/features/mapping/types'
import { completionsAt } from './completions'
import { parseTemplate } from './template'

const spec = {
  specVersion: 2, mode: 'canvas', key: { template: '' }, typeField: { enabled: false, name: '', value: '' }, nullHandling: 'include', fieldMappings: [], relations: [],
  docSchema: {
    source: 'generated',
    root: {
      id: 'fld_root', name: '$', type: 'object',
      children: [
        { id: 'f_id', name: 'orderId', type: 'integer' },
        { id: 'f_cust', name: 'customer', type: 'object', children: [{ id: 'f_first', name: 'firstName', type: 'string' }, { id: 'f_odd', name: 'last name', type: 'string' }] },
        { id: 'f_items', name: 'items', type: 'array', items: { id: 'f_item', name: '[]', type: 'object', children: [{ id: 'f_qty', name: 'qty', type: 'integer' }, { id: 'f_label', name: 'label', type: 'string' }] } },
        { id: 'f_ref', name: 'ref', type: 'string' },
      ],
    },
  },
} as unknown as MappingSpec

const at = (text: string, fieldId = 'f_ref', root = true) => completionsAt(spec, fieldId, text, text.length, ['order_id', 'created_by'], root) ?? { from: -1, items: [] }

describe('template', () => {
  it('parses references and their spans; reports syntax errors', () => {
    const { refs } = parseTemplate('A ${doc.orderId} ${ self.qty }-${parent["last name"]}[${index}] \\${not}')
    expect(refs.map((r) => [r.root, r.segs])).toEqual([['doc', ['orderId']], ['self', ['qty']], ['parent', ['last name']], ['index', []]])
    expect('A ${doc.orderId}'.slice(refs[0]!.start, refs[0]!.end)).toBe('doc.orderId')
    expect(parseTemplate('${doc.a + 1}').error?.message).toContain('only reads values')
    expect(parseTemplate('x ${doc.a').error?.offset).toBe(4)
  })
})

describe('completionsAt', () => {
  it('OC-138 AC3: names in scope, then a container’s fields with their types', () => {
    expect(at('ORD-${').items.map((i) => i.label)).toEqual(['doc', 'self', 'row']) // no parent or index at the root
    expect(at('${d').items.map((i) => i.label)).toEqual(['doc'])
    expect(at('${doc.').items.map((i) => [i.label, i.detail])).toEqual([['orderId', 'integer'], ['customer', 'object'], ['items', 'array<object>']])
    expect(at('${doc.customer.f').items).toEqual([{ label: 'firstName', insert: '.firstName', detail: 'string' }])
    // A name that isn't an identifier is written ["like this"], replacing the dot.
    const odd = at('${doc.customer.la')
    expect(odd.items).toEqual([{ label: 'last name', insert: '["last name"]', detail: 'string' }])
    expect(odd.from).toBe('${doc.customer'.length)
  })

  it('OC-138 AC3: an item field gets self, parent and index; row only at the root', () => {
    expect(at('${', 'f_label', false).items.map((i) => i.label)).toEqual(['doc', 'self', 'parent', 'index'])
    expect(at('${self.', 'f_label', false).items.map((i) => i.label)).toEqual(['qty'])
    expect(at('${parent.cu', 'f_label', false).items.map((i) => i.label)).toEqual(['customer'])
    expect(at('${row.').items.map((i) => i.label)).toEqual(['order_id', 'created_by'])
    expect(at('${row.', 'f_label', false).items).toEqual([])
  })

  it('nothing outside a ${…}', () => {
    expect(completionsAt(spec, 'f_ref', 'plain text', 10, [], true)).toBeNull()
    expect(completionsAt(spec, 'f_ref', '${doc.orderId} and', 18, [], true)).toBeNull()
  })
})
