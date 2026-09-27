import { describe, expect, it } from 'vitest'
import { embedView } from '@/test/mapping'
import { canvasLeaves, embedColumns, embedContainer, embedFieldIds, embedLeafIds, removeEmbed, srcKey } from './embeds'
import { addSource, connect, OpError, replaceSource } from './mappingOps'

const view = embedView()
const spec = view.spec

describe('embeds', () => {
  it('keys, containers and fields', () => {
    expect(srcKey({ column: 'total' })).toBe('total')
    expect(srcKey({ column: 'total', embed: 'emb_o' })).toBe('emb_o:total')
    expect(embedContainer(spec, spec.embeds[0]!)?.id).toBe('f_item')
    expect(embedContainer(spec, { ...spec.embeds[0]!, kind: 'many-to-one' })).toBeUndefined()
    expect([...embedFieldIds(spec)]).toEqual([['f_total', 'emb_o'], ['f_date', 'emb_o']])
    expect(embedLeafIds(spec)).toEqual(['f_total', 'f_date'])
    expect(canvasLeaves(spec).slice(-2).map((l) => l.path)).toEqual(['orders[].total', 'orders[].orderDate'])
    expect(embedColumns(spec, view.embedTables).map((c) => c.name)).toEqual(['emb_o:order_id', 'emb_o:customer_id', 'emb_o:order_date', 'emb_o:total'])
    expect(embedColumns(spec, undefined)).toEqual([])
  })

  it('removes an embed with its field and mappings', () => {
    const next = removeEmbed(spec, 'emb_o')
    expect(next.embeds).toEqual([])
    expect(next.docSchema.root.children?.some((c) => c.id === 'f_orders')).toBe(false)
    expect(next.fieldMappings.some((m) => m.id === 'm_total')).toBe(false)
    expect(removeEmbed(spec, 'nope')).toBe(spec)
  })

  it('embedded columns only fill their embed, and vice versa', () => {
    const [orderDate] = embedColumns(spec, view.embedTables).filter((c) => c.column === 'order_date')
    const res = connect(spec, orderDate!, 'f_date')
    expect(res.outcome).toBe('connected')
    if (res.outcome === 'connected') expect(res.spec.fieldMappings.at(-1)!.sources).toEqual([{ column: 'order_date', embed: 'emb_o' }])
    expect(() => connect(spec, orderDate!, 'f_email')).toThrow(OpError)
    const parentCol = view.embedTables!.emb_o!.columns[0]! // a plain column object, no embed
    expect(() => connect(spec, parentCol, 'f_date')).toThrow(/inside an embedded table/)
    expect(() => replaceSource(spec, 'm_total', parentCol)).toThrow(OpError)
    expect(() => addSource(spec, 'm_total', 'first_name')).toThrow(/same table/)
    const two = addSource(spec, 'm_total', orderDate!)
    expect(two.fieldMappings.find((m) => m.id === 'm_total')!.sources).toHaveLength(2)
  })
})
