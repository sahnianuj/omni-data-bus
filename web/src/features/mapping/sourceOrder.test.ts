import { describe, expect, it } from 'vitest'
import { cartProposal, posSnapshot } from '@/test/designFirst'
import { srcKey } from './embeds'
import { designOrder, orderSources, type SourceRow } from './sourceOrder'
import type { CanvasColumn, MappingSpec } from './types'

const spec = cartProposal.spec as MappingSpec
const table = (name: string) => posSnapshot.schemas[0]!.tables.find((t) => t.name === name)!
const tableColumns: CanvasColumn[] = table('carts').columns
const columns: CanvasColumn[] = [
  ...tableColumns,
  ...spec.relations.flatMap((r) => table(r.table.name).columns.map((c) => ({ ...c, name: srcKey({ column: c.name, relation: r.id }), embed: r.id, column: c.name }))),
]
const all = new Set(columns.map((c) => c.name))
const groups = (rows: SourceRow[]) => rows.flatMap((r) => (r.kind === 'embed' ? [r.embed.table.name] : []))

describe('orderSources (OC-175)', () => {
  it('OC-175 AC1: groups follow the document, nested ones right after their parent', () => {
    const doc = orderSources(spec, tableColumns, columns, all, true, 'document')
    // saved_orders: status (the 4th field) reads it, before customer.
    expect(groups(doc)).toEqual(['saved_orders', 'customers', 'partners', 'cart_items', 'customizations', 'applied_rewards', 'collected_payments'])
    // Creation order puts the lookup first and the total last.
    expect(groups(orderSources(spec, tableColumns, columns, all, true, 'table'))).toEqual(spec.relations.map((r) => r.table.name))

    // A nested relation made after an unrelated one still follows its parent.
    const items = spec.relations.find((r) => r.table.name === 'cart_items')!
    const moved: MappingSpec = { ...spec, relations: [...spec.relations.filter((r) => r.parentId !== items.id), ...spec.relations.filter((r) => r.parentId === items.id)] }
    expect(groups(orderSources(moved, tableColumns, columns, all, true, 'document'))).toEqual(groups(doc))
  })

  it('OC-175 AC2: columns follow the fields they fill; unmapped ones after a divider', () => {
    const rows = orderSources(spec, tableColumns, columns, all, true, 'document')
    const pos = designOrder(spec)
    // Each column's first field, along the rows: never goes back within a scope.
    const firstField = new Map<string, number>()
    for (const m of spec.fieldMappings) for (const s of m.sources) {
      const k = srcKey(s)
      firstField.set(k, Math.min(firstField.get(k) ?? Infinity, pos.get(m.targetFieldId) ?? Infinity))
    }
    let last = -1
    for (const r of rows) {
      if (r.kind === 'divider') break // the root's unmapped columns
      if (r.kind === 'column' && !r.column.embed) {
        const p = firstField.get(r.column.name)
        if (p === undefined) continue // read by an expression (id, status): placed by it, not a source
        expect(p, r.column.name).toBeGreaterThanOrEqual(last)
        last = p
      }
    }
    const root = rows.filter((r): r is Extract<SourceRow, { kind: 'column' }> => r.kind === 'column' && !r.column.embed).map((r) => r.column.name)
    expect(root.slice(0, 3)).toEqual(['id', 'status', 'channel'])
    expect(root.indexOf('total_in_cents')).toBeGreaterThan(root.indexOf('order_notes'))
    const divider = rows.findIndex((r) => r.kind === 'divider' && r.scope === '')
    expect(divider).toBeGreaterThan(0)
    const after = rows.slice(divider + 1).flatMap((r) => (r.kind === 'column' ? [r.column.name] : []))
    expect(after).toContain('receipt_number')
    expect(after).toContain('tax_data')
  })

  it('a filter keeps only the shown columns, and groups with none of them go', () => {
    const shown = new Set(['channel', columns.find((c) => c.column === 'first_name' && c.embed)!.name])
    const rows = orderSources(spec, tableColumns, columns, shown, false, 'document')
    expect(rows.filter((r) => r.kind === 'column')).toHaveLength(2)
    expect(groups(rows)).toHaveLength(1)
  })
})
