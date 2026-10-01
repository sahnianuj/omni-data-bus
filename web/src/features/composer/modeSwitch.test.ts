import { describe, expect, it } from 'vitest'
import type { MappingSpec } from '@/features/mapping/types'
import { composedSpec } from '@/test/composer'
import { addNode, renameNode } from './composerOps'
import { syncBindings, toCanvas, toComposed } from './modeSwitch'

const own = { schema: 'public', name: 'orders' }

/** The canvas form of the composed fixture: no mode, no binding annotations. */
function canvasSpec(): MappingSpec {
  const strip = (f: MappingSpec['docSchema']['root']): MappingSpec['docSchema']['root'] => {
    const rest = { ...f }
    delete rest.binding
    return { ...rest, ...(f.children ? { children: f.children.map(strip) } : {}), ...(f.items ? { items: strip(f.items) } : {}) }
  }
  const s = composedSpec()
  return { ...s, mode: 'canvas', docSchema: { ...s.docSchema, root: strip(s.docSchema.root) } }
}

describe('modeSwitch', () => {
  it('AC6: round trip preserves spec', () => {
    const canvas = canvasSpec()
    const composed = toComposed(canvas)
    expect(composed.mode).toBe('composed')
    // Opening in the composer annotates bound nodes with their relation.
    expect(composed.docSchema.root.children!.find((c) => c.id === 'f_items')!.binding).toEqual({ relationId: 'rel_items' })

    // Edit in the composer, then reopen on the canvas.
    let edited = renameNode(composed, 'f_oid', 'id')
    edited = addNode(edited, 'f_items', 'field', 'note').spec
    const back = toCanvas(edited, own)
    expect('spec' in back).toBe(true)
    const round = (back as { spec: MappingSpec }).spec
    expect(round.mode).toBe('canvas')
    expect(round.relations).toEqual(canvas.relations)
    expect(round.fieldMappings).toEqual(canvas.fieldMappings)
    expect(round.aggregates).toEqual(canvas.aggregates)
    expect(round.fieldMappings.find((m) => m.id === 'm_sku')!.transforms).toEqual([{ op: 'upper' }])
    // Every relation still fills its node.
    for (const r of round.relations.filter((x) => !x.lookupOnly)) expect(JSON.stringify(round.docSchema)).toContain(`"id":"${r.targetFieldId}"`)
  })

  it('a mapping driven by another table stays composed', () => {
    const s = { ...composedSpec(), root: { table: { schema: 'public', name: 'order_items' } } }
    expect(toCanvas(s, own)).toEqual({ blocked: { schema: 'public', name: 'order_items' } })
  })

  it('syncBindings mirrors the relations', () => {
    const s = composedSpec()
    s.relations = s.relations.filter((r) => r.id !== 'rel_cust')
    const synced = syncBindings(s)
    expect(synced.docSchema.root.children!.find((c) => c.id === 'f_cust')!.binding).toBeUndefined()
    expect(syncBindings(synced)).toBe(synced)
  })
})
