import { describe, expect, it } from 'vitest'
import { sampleSpec } from '@/test/mapping'
import { dateFormats, defaultNow, describeNowText, fits, formatSample, nowOf, setNow } from './nowSource'
import type { MappingSpec } from './types'

/** sampleSpec plus a string updatedAt (design format date-time) and an integer migratedAt. */
function spec(): MappingSpec {
  const s = sampleSpec()
  s.docSchema.root.children!.push({ id: 'f_upd', name: 'updatedAt', type: 'string', format: 'date-time' }, { id: 'f_mig', name: 'migratedAt', type: 'integer' })
  return s
}

describe('nowSource (OC-179)', () => {
  it('AC1: the default suits the field: like the document\'s other dates, else its own format or type', () => {
    const s = spec()
    // createdAt is formatDatetime epoch_ms; updatedAt's design says date-time.
    expect(dateFormats(s, 'f_mig').map((d) => [d.name, d.args.format])).toEqual([['createdAt', 'epoch_ms'], ['updatedAt', 'iso8601']])
    expect(defaultNow(s, 'f_mig')).toEqual({ at: 'run', format: 'epoch_ms' }) // same as createdAt
    expect(defaultNow(s, 'f_upd')).toEqual({ at: 'run', format: 'iso8601' }) // epoch doesn't fit a string
    const bare = { ...s, fieldMappings: s.fieldMappings.filter((m) => m.id !== 'm_created') }
    // Without createdAt's format, updatedAt's ISO doesn't fit an integer: by its type, epoch ms.
    expect(dateFormats(bare, 'f_age').map((d) => d.name)).toEqual(['updatedAt'])
    expect(defaultNow(bare, 'f_age')).toEqual({ at: 'run', format: 'epoch_ms' })
    // A string field without a format of its own, in a document with no dates: ISO 8601.
    expect(defaultNow({ ...bare, docSchema: sampleSpec().docSchema }, 'f_email')).toEqual({ at: 'run', format: 'iso8601' })
  })

  it('setNow replaces what filled the field, and edits its own step', () => {
    let s = setNow(spec(), 'f_created', { at: 'write', format: 'custom', pattern: 'yyyy-MM-dd', timezone: 'Asia/Kolkata' })
    const m = s.fieldMappings.filter((x) => x.targetFieldId === 'f_created')
    expect(m).toHaveLength(1)
    expect(m[0]).toMatchObject({ sources: [], transforms: [{ op: 'now', args: { at: 'write', format: 'custom', pattern: 'yyyy-MM-dd', timezone: 'Asia/Kolkata' } }], state: 'active' })
    s = setNow(s, 'f_created', { at: 'run', format: 'epoch_s', timezone: 'Asia/Kolkata' })
    expect(s.fieldMappings.find((x) => x.targetFieldId === 'f_created')!.id).toBe(m[0]!.id)
    expect(nowOf(s, 'f_created')).toEqual({ at: 'run', format: 'epoch_s' }) // an epoch has no time zone
    expect(describeNowText(nowOf(s, 'f_created')!)).toBe('the current date and time (when the run started, epoch seconds)')
  })

  it('formats like the server, and checks the fit', () => {
    const d = new Date(Date.UTC(2026, 9, 17, 14, 3, 22, 400))
    expect(formatSample({ format: 'epoch_ms' }, d)).toBe(d.getTime())
    expect(formatSample({ format: 'epoch_s' }, d)).toBe(Math.floor(d.getTime() / 1000))
    expect(formatSample({ format: 'iso8601' }, d)).toBe('2026-10-17T14:03:22.4Z') // RFC 3339 with trailing zeros dropped
    expect(formatSample({ format: 'iso8601', timezone: 'Asia/Kolkata' }, d)).toBe('2026-10-17T19:33:22.4+05:30')
    expect(formatSample({ format: 'date', timezone: 'America/Los_Angeles' }, d)).toBe('2026-10-17')
    expect(formatSample({ format: 'custom', pattern: "EEE d MMM yyyy 'at' h:mm a", timezone: 'America/New_York' }, d)).toBe('Sat 17 Oct 2026 at 10:03 AM')
    expect(fits('integer', { format: 'iso8601' })).toBe(false)
    expect(fits('integer', { format: 'epoch_ms' })).toBe(true)
    expect(fits('string', { format: 'epoch_ms' })).toBe(false)
    expect(fits('any', { format: 'epoch_ms' })).toBe(true)
  })
})
