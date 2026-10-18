import { describe, expect, it } from 'vitest'
import { findField } from '@/features/docschema/docschemaOps'
import { embedView, sampleSpec } from '@/test/mapping'
import { addToDesign, applyFix, groupIssues, issueTarget, removeBranch, selectionForPath } from './fixes'
import { unusedLookups } from './reads'
import type { FieldMapping, MappingIssue } from './types'

const issue = (over: Partial<MappingIssue>): MappingIssue => ({ severity: 'error', code: 'X', path: 'p', message: 'm', ...over })

describe('applyFix', () => {
  it('mark-optional clears required on the field', () => {
    const next = applyFix(sampleSpec(), issue({ code: 'REQUIRED_FIELD_UNMAPPED', fieldId: 'f_email', fix: { kind: 'mark-optional', label: 'Mark optional' } }))
    expect(findField(next.docSchema, 'f_email')?.field.required).toBeFalsy()
  })

  it('accept-suggestions activates pending mappings', () => {
    const next = applyFix(sampleSpec(), issue({ fix: { kind: 'accept-suggestions', label: 'Accept' } }))
    expect(next.fieldMappings.find((m) => m.id === 'm_email')?.state).toBe('active')
  })

  it('add-transform prepends the step', () => {
    const next = applyFix(sampleSpec(), issue({ fieldMappingId: 'm_first', fix: { kind: 'add-transform', label: 'Add upper', step: { op: 'upper' } } }))
    expect(next.fieldMappings.find((m) => m.id === 'm_first')?.transforms.map((s) => s.op)).toEqual(['upper', 'trim'])
  })

  it('enable-create and missing fixes leave the spec alone', () => {
    const spec = sampleSpec()
    expect(applyFix(spec, issue({ fix: { kind: 'enable-create', label: 'Create' } }))).toBe(spec)
    expect(applyFix(spec, issue({}))).toBe(spec)
  })
})

describe('navigation', () => {
  it('go-to prefers the field mapping', () => {
    expect(issueTarget(issue({ fieldMappingId: 'm1', fieldId: 'f1' }))).toEqual({ kind: 'mapping', id: 'm1' })
    expect(issueTarget(issue({ fieldId: 'f1' }))).toEqual({ kind: 'field', id: 'f1' })
    expect(issueTarget(issue({}))).toBeNull()
  })

  it('jump-to-mapping resolves a dotted path', () => {
    expect(selectionForPath(sampleSpec(), 'name.first')).toEqual({ kind: 'mapping', id: 'm_first' })
    expect(selectionForPath(sampleSpec(), 'age')).toEqual({ kind: 'field', id: 'f_age' })
    expect(selectionForPath(sampleSpec(), 'nope')).toBeNull()
  })

  it('groups by severity', () => {
    const g = groupIssues([issue({ severity: 'warning' }), issue({}), issue({ severity: 'info' })])
    expect([g.error.length, g.warning.length, g.info.length]).toEqual([1, 1, 1])
  })

  it('OC-070 AC2: add to design', () => {
    const base = embedView().spec
    const spec = { ...base, docSchema: { ...base.docSchema, imported: { at: '2026-10-01T00:00:00Z', format: 'sample' as const, samples: 1, fieldCount: 1, fields: [{ path: 'email', type: 'string' as const }] } } }
    const issue: MappingIssue = { severity: 'info', code: 'REFERENCE_EXTRA_FIELD', path: 'orders', nodePath: 'orders', fieldId: 'f_orders', message: '', fix: { kind: 'add-to-design', label: 'Add to design' } }
    const next = applyFix(spec, issue)
    expect(next.docSchema.imported!.fields!.map((f) => `${f.path}:${f.type}`)).toEqual(['email:string', 'orders:array', 'orders[]:object', 'orders[].total:number', 'orders[].orderDate:string'])
    expect(addToDesign(next, 'f_orders')).toBe(next) // already there
    expect(addToDesign(base, 'f_orders')).toBe(base) // no imported design
  })

  it('OC-070 AC3: removing a branch takes its relation along', () => {
    const spec = embedView().spec
    const next = removeBranch(spec, 'f_orders')
    expect(next.relations).toEqual([])
    expect(findField(next.docSchema, 'f_orders')).toBeUndefined()
    expect(next.fieldMappings.some((m) => m.id === 'm_total')).toBe(false)
    // A plain field goes with its mappings.
    const email = spec.fieldMappings.find((m) => findField(spec.docSchema, m.targetFieldId)?.field.name === 'email')!
    const plain = removeBranch(spec, email.targetFieldId)
    expect(findField(plain.docSchema, email.targetFieldId)).toBeUndefined()
    expect(plain.fieldMappings.some((m) => m.id === email.id)).toBe(false)
    expect(plain.relations).toHaveLength(1)
  })

  it('OC-071 AC2: restore and map', () => {
    const base = embedView().spec
    const spec = {
      ...base,
      docSchema: {
        ...base.docSchema,
        imported: { at: '2026-10-01T00:00:00Z', format: 'sample' as const, samples: 1, fieldCount: 4, fields: [{ path: 'orders', type: 'array' as const }, { path: 'orders[]', type: 'object' as const }, { path: 'orders[].total', type: 'number' as const }, { path: 'orders[].orderId', type: 'integer' as const }, { path: 'orders[].orderDate', type: 'string' as const }] },
      },
    }
    const issue: MappingIssue = { severity: 'warning', code: 'REFERENCE_FIELD_UNMAPPED', path: 'orders[].orderId', nodePath: 'orders[].orderId', message: '', fix: { kind: 'restore-field', label: 'Add orderId back (from order_id)', relationId: 'emb_o', columns: ['order_id'] } }
    const next = applyFix(spec, issue)
    const item = findField(next.docSchema, 'f_item')!.field
    expect(item.children!.map((c) => `${c.name}:${c.type}`)).toEqual(['total:number', 'orderId:integer', 'orderDate:string']) // its imported position
    const id = item.children![1]!.id
    expect(next.fieldMappings.find((m) => m.targetFieldId === id)?.sources).toEqual([{ relation: 'emb_o', column: 'order_id' }])
    expect(applyFix(next, issue)).toBe(next) // already back

    const mapped = applyFix(spec, { ...issue, fieldId: 'f_date', fix: { kind: 'map-field', label: 'Map from order_date', relationId: 'emb_o', columns: ['order_date'] } })
    expect(mapped.fieldMappings.filter((m) => m.targetFieldId === 'f_date').map((m) => m.sources)).toEqual([[{ relation: 'emb_o', column: 'order_date' }]])
  })

  it('OC-177 AC1: remove-relation removes an unused lookup; unusedLookups finds it', () => {
    const spec = embedView().spec
    spec.relations.push({ id: 'rel_lk', parentId: null, kind: 'many-to-one', lookupOnly: true, table: { schema: 'shop', name: 'partners' }, join: [{ parent: 'customer_id', child: 'id' }] })
    expect([...unusedLookups(spec)]).toEqual(['rel_lk'])
    const read = { ...spec, fieldMappings: [...spec.fieldMappings, { id: 'm_x', targetFieldId: 'f_age', sources: [], transforms: [{ op: 'expression', args: { expr: 'len(partners.name)' } }], origin: 'manual', state: 'active' } satisfies FieldMapping] }
    expect(unusedLookups(read).size).toBe(0)
    const next = applyFix(spec, issue({ code: 'LOOKUP_UNUSED', severity: 'info', fix: { kind: 'remove-relation', label: 'Remove lookup', relationId: 'rel_lk' } }))
    expect(next.relations.map((r) => r.id)).toEqual(['emb_o'])
  })

  it('OC-179 AC3: replace-step switches a now step\'s format', () => {
    const spec = sampleSpec()
    spec.fieldMappings.push({ id: 'm_now', targetFieldId: 'f_age', sources: [], transforms: [{ op: 'now', args: { at: 'write', format: 'iso8601' } }], origin: 'manual', state: 'active' })
    const next = applyFix(spec, issue({ code: 'NOW_FORMAT_TYPE', fieldMappingId: 'm_now', fix: { kind: 'replace-step', label: 'Use epoch milliseconds', step: { op: 'now', args: { at: 'write', format: 'epoch_ms' } } } }))
    expect(next.fieldMappings.find((m) => m.id === 'm_now')!.transforms).toEqual([{ op: 'now', args: { at: 'write', format: 'epoch_ms' } }])
  })
})
