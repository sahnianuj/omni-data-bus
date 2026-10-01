import { describe, expect, it } from 'vitest'
import type { DocSchema, MappingSpec } from '@/features/mapping/types'
import { composedSpec } from '@/test/composer'
import { foreignKeyOf } from './bindPrefill'
import { acceptSuggestion, addLookup, addNode, applyConstantHint, bind, bindGroupRows, blank, collapseToId, combineRows, pluralName, rebaseSources, deleteNode, expandForeignKey, dismissConstantHint, groupKey, importDesign, moveNode, nodeOf, OpError, setDrivingTable, setExpression, setGroupBy, setNodeType, singularName, unbind } from './composerOps'

const itemsTable = { schema: 'public', name: 'order_items' }

describe('composerOps', () => {
  it('adds nodes under objects and into an array’s items', () => {
    let s: MappingSpec = blank(composedSpec())
    const a = addNode(s, 'fld_root', 'array', 'items')
    s = a.spec
    expect(nodeOf(s, a.id)!.items).toMatchObject({ name: '[]', type: 'object', children: [] })
    const f = addNode(s, a.id, 'field', 'qty')
    expect(nodeOf(f.spec, a.id)!.items!.children!.map((c) => c.name)).toEqual(['qty'])
    expect(() => addNode(f.spec, f.id, 'field')).toThrow(OpError)
  })

  it('binds a node: relation under its scope, adapters by kind, generated fields', () => {
    let s = blank(composedSpec())
    const items = addNode(s, 'fld_root', 'array', 'items')
    s = items.spec
    const res = bind(s, items.id, {
      kind: 'one-to-many', table: itemsTable, join: [{ parent: 'order_id', child: 'order_id' }],
      generate: [{ name: 'line_no', jsonType: 'integer', udtName: 'int4' } as never, { name: 'unit_price', jsonType: 'number', udtName: 'numeric' } as never],
    })
    const rel = res.spec.relations.find((r) => r.id === res.relationId)!
    expect(rel).toMatchObject({ parentId: null, targetFieldId: items.id, kind: 'one-to-many' })
    expect(nodeOf(res.spec, items.id)!.binding).toEqual({ relationId: res.relationId })
    expect(nodeOf(res.spec, items.id)!.items!.children!.map((c) => [c.name, c.type])).toEqual([['lineNo', 'integer'], ['unitPrice', 'number']])

    // A nested object binds under the array's relation; an object on one-to-many gets "first".
    const prod = addNode(res.spec, items.id, 'object', 'product')
    const nested = bind(prod.spec, prod.id, { kind: 'many-to-one', table: { schema: 'public', name: 'products' }, join: [{ parent: 'product_id', child: 'product_id' }] })
    expect(nested.spec.relations.find((r) => r.id === nested.relationId)!.parentId).toBe(res.relationId)
    const last = addNode(nested.spec, 'fld_root', 'object', 'lastLine')
    const first = bind(last.spec, last.id, { kind: 'one-to-many', table: itemsTable, join: [{ parent: 'order_id', child: 'order_id' }] })
    expect(nodeOf(first.spec, last.id)!.binding!.adapter).toBe('first')

    // Rebinding keeps the relation id; unbinding drops it with what's nested and mapped under it.
    const again = bind(first.spec, items.id, { kind: 'one-to-many', table: itemsTable, join: [{ parent: 'order_id', child: 'order_id' }], limit: 10 })
    expect(again.relationId).toBe(res.relationId)
    const un = unbind(again.spec, items.id)
    expect(un.relations.map((r) => r.id)).toEqual([first.relationId])
    expect(nodeOf(un, items.id)!.binding).toBeUndefined()
  })

  it('deletes a node with its mappings, bindings and aggregates', () => {
    const s = deleteNode(composedSpec(), 'f_items')
    expect(s.relations.map((r) => r.id)).toEqual(['rel_cust', 'rel_tags'])
    expect(s.aggregates).toEqual([])
    expect(s.fieldMappings.map((m) => m.id)).toEqual(['m_oid', 'm_cname', 'm_label'])
  })

  /** What the server returns for {"type":"order","orderId":1,"items":[{"sku":"A","qty":2}]} (OC-059). */
  const inferred = (): DocSchema => ({
    source: 'json-sample',
    reference: { type: 'order', orderId: 1, items: [{ sku: 'A', qty: 2 }] },
    imported: { at: '2026-09-30T00:00:00Z', format: 'sample', samples: 1, fieldCount: 5 },
    constantHints: [{ fieldId: 'i_type', value: 'order', reason: 'discriminator-name' }],
    root: {
      id: 'fld_root', name: '$', type: 'object',
      children: [
        { id: 'i_type', name: 'type', type: 'string', required: true, example: 'order' },
        { id: 'i_oid', name: 'orderId', type: 'integer', required: true, example: 1 },
        { id: 'i_items', name: 'items', type: 'array', required: true, items: { id: 'i_item', name: '[]', type: 'object', children: [{ id: 'i_sku', name: 'sku', type: 'string', example: 'A' }, { id: 'i_qty', name: 'qty', type: 'integer', example: 2 }] } },
      ],
    },
  })

  it('OC-059: importing replaces the design with the server’s, reference and hints included', () => {
    const res = importDesign(composedSpec(), inferred(), null)
    expect(res.added).toBe(5)
    expect(res.spec.docSchema).toEqual(inferred())
    expect(res.spec.relations).toEqual([])
    expect(res.spec.fieldMappings).toEqual([])
    expect(res.spec.aggregates).toEqual([])
    expect(res.spec.root).toEqual(composedSpec().root) // the driving table stays
  })

  it('AC5: import onto node keeps mapped fields', () => {
    const onto: DocSchema = {
      source: 'json-sample',
      constantHints: [{ fieldId: 'n_cur', value: 'EUR', reason: 'same-in-every-sample' }, { fieldId: 'n_sku', value: 'X', reason: 'same-in-every-sample' }],
      root: { id: 'fld_root', name: '$', type: 'object', children: [{ id: 'n_sku', name: 'sku', type: 'integer' }, { id: 'n_disc', name: 'discount', type: 'number' }, { id: 'n_cur', name: 'currency', type: 'string' }] },
    }
    const before = composedSpec()
    const res = importDesign(before, onto, 'f_items')
    expect(res.kept).toEqual(['sku'])
    expect(res.added).toBe(2)
    const kids = nodeOf(res.spec, 'f_items')!.items!.children!
    expect(kids.map((c) => c.name)).toEqual(['lineNo', 'sku', 'product', 'discount', 'currency'])
    // sku keeps its id, type, mapping; the binding is untouched.
    expect(kids[1]).toEqual({ id: 'f_sku', name: 'sku', type: 'string' })
    expect(res.spec.fieldMappings).toEqual(before.fieldMappings)
    expect(res.spec.relations).toEqual(before.relations)
    expect(nodeOf(res.spec, 'f_items')!.binding).toEqual({ relationId: 'rel_items' })
    // Only the added fields' hints come along.
    expect(res.spec.docSchema.constantHints).toEqual([{ fieldId: 'n_cur', value: 'EUR', reason: 'same-in-every-sample' }])
    // A scalar field can't take an import.
    expect(() => importDesign(before, onto, 'f_oid')).toThrow(OpError)
  })

  it('OC-059 AC4: a constant hint becomes a constant mapping, or is dismissed', () => {
    const s = importDesign(composedSpec(), inferred(), null).spec
    const kept = applyConstantHint(s, 'i_type')
    const m = kept.fieldMappings.find((x) => x.targetFieldId === 'i_type')!
    expect(m).toMatchObject({ sources: [], transforms: [{ op: 'constant', args: { value: 'order' } }], state: 'active' })
    expect(kept.docSchema.constantHints).toBeUndefined()
    const gone = dismissConstantHint(s, 'i_type')
    expect(gone.docSchema.constantHints).toBeUndefined()
    expect(gone.fieldMappings).toEqual([])
  })

  it('moves, retypes, looks up, drives and computes', () => {
    let s = composedSpec()
    s = moveNode(s, 'f_count', -1)
    expect(s.docSchema.root.children!.map((c) => c.id).slice(0, 2)).toEqual(['f_count', 'f_oid'])
    s = setNodeType(s, 'f_cust', 'string')
    expect(s.relations.some((r) => r.id === 'rel_cust')).toBe(false)
    const lk = addLookup(s, 'f_items', { table: { schema: 'public', name: 'customers' }, join: [{ parent: 'order_id', child: 'customer_id' }] })
    expect(lk.spec.relations.at(-1)).toMatchObject({ lookupOnly: true, parentId: 'rel_items' })
    expect(setDrivingTable(s, itemsTable).root).toEqual({ table: itemsTable })
    const e = setExpression(s, 'f_sku', 'concat($root.order_no, "-", line_no)')
    const m = e.fieldMappings.find((x) => x.targetFieldId === 'f_sku' && x.state === 'active')!
    expect(m.id).toBe('m_sku')
    expect(m.sources).toEqual([])
    expect(m.transforms).toEqual([{ op: 'expression', args: { expr: 'concat($root.order_no, "-", line_no)' } }])
  })

  describe('OC-063: one document per group', () => {
    const detail = { schema: 'public', name: 'order_detail' }
    const design = (): MappingSpec => {
      let s: MappingSpec = { ...blank(composedSpec()), root: { table: detail } }
      s = addNode(s, 'fld_root', 'field', 'orderId').spec
      return addNode(s, 'fld_root', 'array', 'products').spec
    }
    const products = (s: MappingSpec) => s.docSchema.root.children!.find((c) => c.name === 'products')!.id

    it('groups, binds the group rows, and keeps them in sync', () => {
      expect(() => bindGroupRows(design(), products(design()))).toThrow(OpError) // not grouped yet
      let s = setGroupBy(design(), ['order_id'], detail)
      expect(s.root).toEqual({ table: detail, groupBy: ['order_id'] })
      const res = bindGroupRows(s, products(s), [{ column: 'line_no' }])
      s = res.spec
      expect(s.relations.find((r) => r.id === res.relationId)).toMatchObject({ group: true, kind: 'one-to-many', table: detail, join: [{ parent: 'order_id', child: 'order_id' }], orderBy: [{ column: 'line_no' }], parentId: null })
      // Two group columns: the join follows.
      s = setGroupBy(s, ['type', 'order_id'], detail)
      expect(s.relations.find((r) => r.group)!.join).toEqual([{ parent: 'type', child: 'type' }, { parent: 'order_id', child: 'order_id' }])
      // Back to one per row, or another driving table: the group rows go.
      expect(setGroupBy(s, [], detail).relations.some((r) => r.group)).toBe(false)
      expect(setDrivingTable(s, { schema: 'public', name: 'orders' })).toMatchObject({ root: { table: { name: 'orders' } }, relations: [] })
    })

    it('only one array holds the group rows, directly under the root', () => {
      let s = setGroupBy(design(), ['order_id'], detail)
      s = bindGroupRows(s, products(s)).spec
      const other = addNode(s, 'fld_root', 'array', 'lines')
      s = bindGroupRows(other.spec, other.id).spec
      expect(s.relations.filter((r) => r.group).map((r) => r.targetFieldId)).toEqual([other.id])
      const nested = addNode(s, other.id, 'array', 'inner')
      expect(() => bindGroupRows(nested.spec, nested.id)).toThrow(OpError)
    })

    it('keeps the key on the group columns', () => {
      expect(groupKey('order_detail::{order_id}::{line_no}', ['order_id'])).toBe('order_detail::{order_id}')
      expect(groupKey('order::{order_id}', ['order_id'])).toBe('order::{order_id}')
      expect(groupKey('od::{order_id|trim}-{line_no}', ['type', 'order_id'])).toBe('od::{type}::{order_id}')
      expect(groupKey('{$uuid}', ['order_id'])).toBe('{order_id}')
      expect(setGroupBy({ ...design(), key: { template: 'order_detail::{order_id}::{line_no}' } }, ['order_id'], detail).key.template).toBe('order_detail::{order_id}')
    })

    it('accepts a group suggestion in one step', () => {
      const s = design()
      const out = acceptSuggestion(s, products(s), { group: true, groupBy: ['order_id'], orderBy: [{ column: 'line_no' }], kind: 'one-to-many', table: detail })
      expect(out.root?.groupBy).toEqual(['order_id'])
      expect(out.relations.find((r) => r.group)).toMatchObject({ targetFieldId: products(s), orderBy: [{ column: 'line_no' }] })
    })
  })

  describe('OC-064: expand a foreign key into an object, and back', () => {
    const detail = { schema: 'public', name: 'order_detail' }
    const products = { schema: 'public', name: 'products' }
    const fk = { table: products, join: [{ parent: 'product_id', child: 'product_id' }] }
    /** products[] ← the group's rows, with lineNo and productId mapped from them. */
    const grouped = () => {
      let s: MappingSpec = setGroupBy({ ...blank(composedSpec()), root: { table: detail } }, ['order_id'], detail)
      const arr = addNode(s, 'fld_root', 'array', 'products')
      const g = bindGroupRows(arr.spec, arr.id, [{ column: 'line_no' }])
      s = g.spec
      const line = addNode(s, arr.id, 'field', 'lineNo')
      const pid = addNode(line.spec, arr.id, 'field', 'productId')
      s = pid.spec
      const m = (id: string, field: string, column: string) => ({ id, targetFieldId: field, sources: [{ relation: g.relationId, column }], transforms: [], origin: 'manual' as const, state: 'active' as const })
      s = { ...s, fieldMappings: [m('m_line', line.id, 'line_no'), m('m_pid', pid.id, 'product_id')] }
      return { s, arr: arr.id, group: g.relationId, pid: pid.id }
    }
    const kids = (s: MappingSpec, arr: string) => nodeOf(s, arr)!.items!.children!.map((c) => c.name)
    const cols = [{ name: 'sku', jsonType: 'string', udtName: 'text' } as never, { name: 'name', jsonType: 'string', udtName: 'text' } as never]

    it('AC1: expand foreign key, replace', () => {
      const { s, arr, group, pid } = grouped()
      expect(singularName('products')).toBe('product')
      const res = expandForeignKey(s, pid, fk, { mode: 'replace', name: 'product', columns: cols })
      expect(kids(res.spec, arr)).toEqual(['lineNo', 'product']) // where productId was
      const rel = res.spec.relations.find((r) => r.targetFieldId === res.id)!
      expect(rel).toMatchObject({ kind: 'many-to-one', table: products, join: fk.join, parentId: group })
      expect(nodeOf(res.spec, res.id)!.children!.map((c) => c.name)).toEqual(['sku', 'name'])
      expect(res.spec.fieldMappings.map((m) => m.id)).toEqual(['m_line']) // productId's mapping went with it
    })

    it('AC2: alongside', () => {
      const { s, arr, pid } = grouped()
      const res = expandForeignKey(s, pid, fk, { mode: 'alongside', name: 'product', columns: cols })
      expect(kids(res.spec, arr)).toEqual(['lineNo', 'productId', 'product'])
      expect(res.spec.fieldMappings.map((m) => m.id)).toEqual(['m_line', 'm_pid'])
    })

    it('AC3: collapse to id', () => {
      const { s, arr, group, pid } = grouped()
      const embedded = expandForeignKey(s, pid, fk, { mode: 'replace', name: 'product', columns: cols })
      const back = collapseToId(embedded.spec, embedded.id)
      expect(kids(back, arr)).toEqual(['lineNo', 'productId'])
      expect(nodeOf(back, embedded.id)).toMatchObject({ name: 'productId', type: 'integer' })
      expect(nodeOf(back, embedded.id)!.children).toBeUndefined()
      expect(back.relations.map((r) => r.id)).toEqual([group])
      expect(back.fieldMappings.find((m) => m.targetFieldId === embedded.id)!.sources).toEqual([{ relation: group, column: 'product_id' }])
      expect(() => collapseToId(back, arr)).toThrow(OpError) // a one-to-many array isn't a key
    })

    it('AC4: offered on relationships only', () => {
      const { s, pid } = grouped()
      const hop = { from: detail, to: products, kind: 'many-to-one', join: fk.join, indexed: true }
      const graph = { root: detail, depth: 2, paths: [{ hops: [hop] }], junctions: [], advice: [] } as never
      expect(foreignKeyOf(graph, s, pid, detail)).toEqual({ table: products, join: fk.join, virtual: undefined })
      const line = s.fieldMappings.find((m) => m.id === 'm_line')!.targetFieldId
      expect(foreignKeyOf(graph, s, line, detail)).toBeUndefined() // line_no isn't on a relationship
      const virtual = { root: detail, depth: 2, paths: [{ hops: [{ ...hop, virtual: true }] }], junctions: [], advice: [] } as never
      expect(foreignKeyOf(virtual, s, pid, detail)?.virtual).toBe(true)
    })
  })

  it('OC-065 AC3: bind rebases', () => {
    const detail = { schema: 'public', name: 'order_detail' }
    let s: MappingSpec = setGroupBy({ ...blank(composedSpec()), root: { table: detail } }, ['order_id'], detail)
    const arr = addNode(s, 'fld_root', 'array', 'products')
    const line = addNode(arr.spec, arr.id, 'field', 'lineNo')
    s = line.spec
    // Mapped from the driving table before the array was bound: the order's first row.
    s = { ...s, fieldMappings: [{ id: 'm_line', targetFieldId: line.id, sources: [{ column: 'line_no' }], transforms: [], origin: 'manual', state: 'active' }] }
    const g = bindGroupRows(s, arr.id, [{ column: 'line_no' }])
    expect(g.spec.fieldMappings[0]!.sources).toEqual([{ column: 'line_no', relation: g.relationId }])
    // Rows of another table keep a parent value: orderId on each order_items line.
    const other = composedSpec()
    other.fieldMappings = other.fieldMappings.map((m) => (m.id === 'm_line' ? { ...m, sources: [{ column: 'order_id' }] } : m))
    expect(rebaseSources(other, 'rel_items')).toEqual(other)
  })

  it('OC-066 AC1: combine rows', () => {
    // The flat order_detail mapping keyed order::{order_id}: rows collide on order_id.
    const detail = { schema: 'public', name: 'order_detail' }
    let s: MappingSpec = { ...blank(composedSpec()), root: { table: detail }, key: { template: 'order::{order_id}' } }
    const ids: Record<string, string> = {}
    const cols: [string, string][] = [['type', 'type'], ['orderId', 'order_id'], ['lineNo', 'line_no'], ['productId', 'product_id'], ['quantity', 'quantity']]
    for (const [name, col] of cols) {
      const f = addNode(s, 'fld_root', 'field', name)
      ids[name] = f.id
      s = { ...f.spec, fieldMappings: [...f.spec.fieldMappings, { id: `m_${name}`, targetFieldId: f.id, sources: [{ column: col }], transforms: [], origin: 'manual', state: 'active' }] }
    }
    expect([pluralName('order_detail'), pluralName('order_items'), pluralName('category'), pluralName('address')]).toEqual(['orderDetails', 'orderItems', 'categories', 'addresses'])
    const res = combineRows(s, { groupBy: ['order_id'], driving: detail, arrayName: 'products', orderBy: [{ column: 'line_no' }] })
    const out = res.spec
    expect(out.root).toEqual({ table: detail, groupBy: ['order_id'] })
    // type, the same on every row, stays on the document; the line columns move into the rows.
    expect(out.docSchema.root.children!.map((c) => c.name)).toEqual(['type', 'orderId', 'products'])
    expect(nodeOf(out, res.arrayId)!.items!.children!.map((c) => [c.name, c.id])).toEqual([['lineNo', ids.lineNo], ['productId', ids.productId], ['quantity', ids.quantity]])
    const group = out.relations.find((r) => r.group)!
    expect(group).toMatchObject({ targetFieldId: res.arrayId, orderBy: [{ column: 'line_no' }] })
    expect(out.fieldMappings.find((m) => m.id === 'm_lineNo')!.sources).toEqual([{ column: 'line_no', relation: group.id }])
    expect(out.fieldMappings.find((m) => m.id === 'm_orderId')!.sources).toEqual([{ column: 'order_id' }])
    expect(out.key.template).toBe('order::{order_id}')

    // Into an existing (imported) array.
    const arr = addNode(s, 'fld_root', 'array', 'items')
    const into = combineRows(arr.spec, { groupBy: ['order_id'], driving: detail, arrayName: 'x', arrayId: arr.id, orderBy: [] })
    expect(into.arrayId).toBe(arr.id)
    expect(nodeOf(into.spec, arr.id)!.items!.children!.map((c) => c.name)).toEqual(['lineNo', 'productId', 'quantity'])
    // An explicit choice wins.
    const chosen = combineRows(s, { groupBy: ['order_id'], driving: detail, arrayName: 'products', orderBy: [], move: [ids.quantity!] })
    expect(nodeOf(chosen.spec, chosen.arrayId)!.items!.children!.map((c) => c.name)).toEqual(['quantity'])
  })
})
