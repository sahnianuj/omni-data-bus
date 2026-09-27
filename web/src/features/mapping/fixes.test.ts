import { describe, expect, it } from 'vitest'
import { findField } from '@/features/docschema/docschemaOps'
import { sampleSpec } from '@/test/mapping'
import { applyFix, groupIssues, issueTarget, selectionForPath } from './fixes'
import type { MappingIssue } from './types'

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
})
