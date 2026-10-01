import { describe, expect, it } from 'vitest'
import { embedView } from '@/test/mapping'
import { canvasLeaves, embedColumns, embedContainer, embedFieldIds, embedLeafIds, fieldScopes, inScope, removeEmbed, srcKey } from './embeds'
import { addSource, connect, OpError, replaceSource } from './mappingOps'

const view = embedView()
const spec = view.spec

describe('embeds', () => {
  it('keys, containers and fields', () => {
    expect(srcKey({ column: 'total' })).toBe('total')
    expect(srcKey({ column: 'total', relation: 'emb_o' })).toBe('emb_o:total')
    expect(srcKey({ column: 'qty', relation: 'emb_o', from: 'via' })).toBe('emb_o:via:qty')
    expect(embedContainer(spec, spec.relations[0]!)?.id).toBe('f_item')
    expect(embedContainer(spec, { ...spec.relations[0]!, kind: 'many-to-one' })).toBeUndefined()
    expect([...embedFieldIds(spec)]).toEqual([['f_total', 'emb_o'], ['f_date', 'emb_o']])
    expect(embedLeafIds(spec)).toEqual(['f_total', 'f_date'])
    expect(canvasLeaves(spec).slice(-2).map((l) => l.path)).toEqual(['orders[].total', 'orders[].orderDate'])
    expect(embedColumns(spec, view.embedTables).map((c) => c.name)).toEqual(['emb_o:order_id', 'emb_o:customer_id', 'emb_o:order_date', 'emb_o:total'])
    expect(embedColumns(spec, undefined)).toEqual([])
  })

  it('scopes nest: a nested relation reads its ancestors and their lookups, not its siblings', () => {
    const nested = {
      ...spec,
      relations: [
        ...spec.relations,
        { id: 'emb_p', parentId: 'emb_o', kind: 'many-to-one' as const, table: { schema: 'shop', name: 'product' }, join: [], targetFieldId: 'f_prod' },
        { id: 'emb_c', parentId: null, kind: 'many-to-one' as const, lookupOnly: true, table: { schema: 'shop', name: 'country' }, join: [] },
      ],
    }
    const item = nested.docSchema.root.children!.find((c) => c.id === 'f_orders')!.items!
    item.children = [...(item.children ?? []), { id: 'f_prod', name: 'product', type: 'object', children: [{ id: 'f_sku', name: 'sku', type: 'string' }] }]
    expect(fieldScopes(nested).get('f_sku')).toBe('emb_p')
    expect(inScope(nested, 'emb_p', 'emb_o')).toBe(true)
    expect(inScope(nested, 'emb_p', 'emb_c')).toBe(true)
    expect(inScope(nested, '', 'emb_p')).toBe(false)
    expect(embedContainer(nested, nested.relations[1]!)?.id).toBe('f_prod')
    const without = removeEmbed(nested, 'emb_o')
    expect(without.relations.map((r) => r.id)).toEqual(['emb_c']) // nested relations go with their parent
  })

  it('removes an embed with its field and mappings', () => {
    const next = removeEmbed(spec, 'emb_o')
    expect(next.relations).toEqual([])
    expect(next.docSchema.root.children?.some((c) => c.id === 'f_orders')).toBe(false)
    expect(next.fieldMappings.some((m) => m.id === 'm_total')).toBe(false)
    expect(removeEmbed(spec, 'nope')).toBe(spec)
  })

  it('OC-069 AC2: removing a lookup removes its fields', () => {
    const lookup = {
      ...spec,
      docSchema: {
        ...spec.docSchema,
        root: {
          ...spec.docSchema.root,
          children: [
            ...(spec.docSchema.root.children ?? []),
            { id: 'f_sku', name: 'addressCity', type: 'string' as const },
            { id: 'f_zip', name: 'addressZip', type: 'string' as const },
          ],
        },
      },
      relations: [...spec.relations, { id: 'emb_a', parentId: null, kind: 'many-to-one' as const, lookupOnly: true, table: { schema: 'shop', name: 'address' }, join: [{ parent: 'address_id', child: 'address_id' }] }],
      fieldMappings: [
        ...spec.fieldMappings,
        { id: 'm_city', targetFieldId: 'f_sku', sources: [{ relation: 'emb_a', column: 'city' }], transforms: [], origin: 'auto' as const, state: 'active' as const, confidence: 1 },
        { id: 'm_zip', targetFieldId: 'f_zip', sources: [{ relation: 'emb_a', column: 'zip' }], transforms: [], origin: 'auto' as const, state: 'active' as const, confidence: 1 },
        // Also fed by a root column: the field stays, without the lookup's mapping.
        { id: 'm_zip2', targetFieldId: 'f_zip', sources: [{ column: 'email' }], transforms: [], origin: 'manual' as const, state: 'active' as const, confidence: 1 },
      ],
    }
    const next = removeEmbed(lookup, 'emb_a')
    const ids = (next.docSchema.root.children ?? []).map((c) => c.id)
    expect(ids).not.toContain('f_sku')
    expect(ids).toContain('f_zip')
    expect(ids).toContain('f_orders')
    expect(next.fieldMappings.map((m) => m.id)).not.toContain('m_city')
    expect(next.fieldMappings.map((m) => m.id)).toContain('m_zip2')

    // A field the imported design defines stays, unmapped.
    const imported = { ...lookup, docSchema: { ...lookup.docSchema, imported: { at: '2026-10-01T00:00:00Z', format: 'sample' as const, samples: 1, fieldCount: 1, fields: [{ path: 'addressCity', type: 'string' as const }] } } }
    expect((removeEmbed(imported, 'emb_a').docSchema.root.children ?? []).map((c) => c.id)).toContain('f_sku')
  })

  it('embedded columns only fill their embed, and vice versa', () => {
    const [orderDate] = embedColumns(spec, view.embedTables).filter((c) => c.column === 'order_date')
    const res = connect(spec, orderDate!, 'f_date')
    expect(res.outcome).toBe('connected')
    if (res.outcome === 'connected') expect(res.spec.fieldMappings.at(-1)!.sources).toEqual([{ column: 'order_date', relation: 'emb_o' }])
    expect(() => connect(spec, orderDate!, 'f_email')).toThrow(OpError)
    // A root column can fill a field inside the embed (items[].orderId ← $root, OC-043 AC4).
    const parentCol = view.embedTables!.emb_o!.columns[0]! // a plain column object, no embed
    const root = connect(spec, parentCol, 'f_date')
    expect(root.outcome).not.toBe('error')
    expect(replaceSource(spec, 'm_total', parentCol).fieldMappings.find((m) => m.id === 'm_total')!.sources).toEqual([{ column: parentCol.name }])
    const two = addSource(spec, 'm_total', orderDate!)
    expect(two.fieldMappings.find((m) => m.id === 'm_total')!.sources).toHaveLength(2)
    expect(addSource(spec, 'm_total', 'first_name').fieldMappings.find((m) => m.id === 'm_total')!.sources).toHaveLength(2)
  })
})
