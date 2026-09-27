import { describe, expect, it } from 'vitest'
import { leaves } from '@/features/docschema/docschemaOps'
import { columns, sampleSpec } from '@/test/mapping'
import { selectVisible } from './selectVisible'

describe('selectVisible', () => {
  it('AC6: filter + search combine', () => {
    const spec = sampleSpec()
    const ls = leaves(spec.docSchema)
    const unmapped = selectVisible(spec, columns, ls, 'unmapped', '')
    expect(unmapped.sources).toEqual(['last_name', 'loyalty_tier', 'age_text'])
    expect(unmapped.targets).toEqual(['f_last', 'f_email', 'f_age', 'f_profile'].filter((id) => id !== 'f_email'))
    expect(selectVisible(spec, columns, ls, 'suggested', '').targets).toEqual(['f_email'])
    expect(selectVisible(spec, columns, ls, 'required', '').targets).toEqual(['f_id', 'f_email'])
    expect(selectVisible(spec, columns, ls, 'manual', '').sources).toEqual(['created_on'])

    // Search keeps the counterparts of matches: "first" matches name.first and pulls in first_name.
    const s = selectVisible(spec, columns, ls, 'all', 'name.first')
    expect(s.targets).toEqual(['f_first'])
    expect(s.sources).toEqual(['first_name'])
    const both = selectVisible(spec, columns, ls, 'all', 'email')
    expect(both.sources).toEqual(['email_address'])
    expect(both.targets).toEqual(['f_email'])
    const errs = selectVisible(spec, columns, ls, 'errors', '', [{ severity: 'error', code: 'X', path: 'email', message: '', fieldId: 'f_email' }])
    expect(errs.targets).toEqual(['f_email'])
    expect(errs.sources).toEqual(['email_address'])
    expect(unmapped.sourceStates.get('legacy_flag')).toBe('ignored')
  })
})
