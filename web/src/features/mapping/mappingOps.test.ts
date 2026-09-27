import { describe, expect, it } from 'vitest'
import { columns, sampleSpec, scol } from '@/test/mapping'
import { findField } from '@/features/docschema/docschemaOps'
import { acceptAll, acceptSuggestion, addSource, connect, createFieldAndMap, disconnect, ignoreColumn, OpError, rejectSuggestion, replaceSource, setConstant, setTransforms, unignoreColumn, unmap } from './mappingOps'

const col = (n: string) => columns.find((c) => c.name === n)!

describe('mappingOps', () => {
  it('AC2: connect creates manual mapping', () => {
    const res = connect(sampleSpec(), col('last_name'), 'f_last')
    expect(res.outcome).toBe('connected')
    if (res.outcome !== 'connected') return
    const m = res.spec.fieldMappings.find((x) => x.id === res.mappingId)!
    expect(m).toMatchObject({ targetFieldId: 'f_last', sources: [{ column: 'last_name' }], origin: 'manual', state: 'active', transforms: [] })
    const lossy = connect(sampleSpec(), col('age_text'), 'f_age')
    expect(lossy).toMatchObject({ outcome: 'connected', class: 'lossy' })
    if (lossy.outcome === 'connected') expect(lossy.spec.fieldMappings.at(-1)!.transforms).toEqual([{ op: 'parseInteger' }])
    expect(() => connect(sampleSpec(), col('first_name'), 'f_profile')).toThrow(OpError)
    expect(connect(sampleSpec(), col('last_name'), 'f_id')).toEqual({ outcome: 'occupied', mappingId: 'm_id' })
    // A pending suggestion for the field is superseded.
    const over = connect(sampleSpec(), col('last_name'), 'f_email')
    if (over.outcome === 'connected') expect(over.spec.fieldMappings.some((m) => m.id === 'm_email')).toBe(false)
    expect(() => connect(sampleSpec(), col('last_name'), 'nope')).toThrow('Unknown target')
  })

  it('AC3: addSource appends and sets concat', () => {
    const s = addSource(sampleSpec(), 'm_first', 'last_name')
    const m = s.fieldMappings.find((x) => x.id === 'm_first')!
    expect(m.sources.map((x) => x.column)).toEqual(['first_name', 'last_name'])
    expect(m.transforms[0]).toEqual({ op: 'concat', args: { separator: ' ' } })
    expect(m.transforms[1]).toEqual({ op: 'trim' })
    expect(addSource(s, 'm_first', 'last_name').fieldMappings.find((x) => x.id === 'm_first')!.sources).toHaveLength(2)
  })

  it('AC3: replaceSource keeps transforms that remain valid', () => {
    const s = replaceSource(sampleSpec(), 'm_first', col('last_name'))
    expect(s.fieldMappings.find((x) => x.id === 'm_first')).toMatchObject({ sources: [{ column: 'last_name' }], transforms: [{ op: 'trim' }], origin: 'manual' })
    // The old default conversion (datetime → integer) is replaced by the new source's.
    const r = replaceSource(sampleSpec(), 'm_created', col('age_text'))
    expect(r.fieldMappings.find((x) => x.id === 'm_created')!.transforms).toEqual([{ op: 'parseInteger' }])
    expect(() => replaceSource(sampleSpec(), 'm_id', scol('doc', 'string'))).not.toThrow()
  })

  it('AC4: createFieldAndMap at root and nested', () => {
    const root = createFieldAndMap(sampleSpec(), col('loyalty_tier'), 'fld_root')
    expect(findField(root.spec.docSchema, root.fieldId)).toMatchObject({ path: 'loyaltyTier', field: { type: 'string' } })
    const nested = createFieldAndMap(sampleSpec(), col('loyalty_tier'), 'f_profile', 'snake_case')
    expect(findField(nested.spec.docSchema, nested.fieldId)!.path).toBe('profile.loyalty_tier')
    expect(nested.spec.fieldMappings.at(-1)).toMatchObject({ targetFieldId: nested.fieldId, origin: 'manual' })
    const req = createFieldAndMap(sampleSpec(), col('customer_id'), 'fld_root')
    expect(findField(req.spec.docSchema, req.fieldId)!.field.required, 'NOT NULL → required').toBe(true)
  })

  it('AC5: disconnect manual removes, auto rejects', () => {
    expect(disconnect(sampleSpec(), 'm_created').fieldMappings.some((m) => m.id === 'm_created')).toBe(false)
    expect(disconnect(sampleSpec(), 'm_id').fieldMappings.find((m) => m.id === 'm_id')!.state).toBe('rejected')
    expect(disconnect(sampleSpec(), 'nope')).toEqual(sampleSpec())
    const unmapped = unmap(sampleSpec(), 'f_first')
    expect(unmapped.fieldMappings.find((m) => m.id === 'm_first')!.state).toBe('rejected')
  })

  it('AC4: acceptSuggestion/rejectSuggestion reducers', () => {
    const acc = acceptSuggestion(sampleSpec(), 'm_email').fieldMappings.find((m) => m.id === 'm_email')!
    expect(acc).toMatchObject({ state: 'active', origin: 'manual', reason: 'accepted-suggestion' })
    expect(rejectSuggestion(sampleSpec(), 'm_email').fieldMappings.find((m) => m.id === 'm_email')!.state).toBe('rejected')
    expect(acceptAll(sampleSpec(), 0.8).fieldMappings.find((m) => m.id === 'm_email')!.state, '0.78 < 0.8').toBe('pending')
    expect(acceptAll(sampleSpec()).fieldMappings.find((m) => m.id === 'm_email')!.state).toBe('active')
  })

  it('AC6: ignore, constants and transforms', () => {
    const ign = ignoreColumn(sampleSpec(), 'first_name')
    expect(ign.ignoredColumns).toContain('first_name')
    expect(ign.fieldMappings.some((m) => m.id === 'm_first'), 'its mapping goes too').toBe(false)
    expect(ignoreColumn(ign, 'first_name')).toBe(ign)
    expect(unignoreColumn(ign, 'first_name').ignoredColumns).not.toContain('first_name')
    const c = setConstant(sampleSpec(), 'f_last', 'n/a')
    expect(c.fieldMappings.at(-1)).toMatchObject({ targetFieldId: 'f_last', sources: [], transforms: [{ op: 'constant', args: { value: 'n/a' } }] })
    expect(setTransforms(sampleSpec(), 'm_first', [{ op: 'upper' }]).fieldMappings.find((m) => m.id === 'm_first')!.transforms).toEqual([{ op: 'upper' }])
  })
})
