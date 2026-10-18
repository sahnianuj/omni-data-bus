import { fieldsById } from './embeds'
import { newMappingId } from './mappingOps'
import type { DocField, FieldType, MappingSpec } from './types'

/** A now step's arguments (OC-179): when, and formatDatetime's format, pattern and time zone. */
export type NowArgs = { at?: 'run' | 'write'; format?: 'iso8601' | 'date' | 'epoch_ms' | 'epoch_s' | 'custom'; pattern?: string; timezone?: string }

/** A date format another field of the document uses ("Same as createdAt"). */
export type DateFormatRef = { fieldId: string; name: string; args: NowArgs }

/** Fields that can hold the current date and time: scalars other than booleans. */
export const canHoldNow = (t: FieldType | undefined) => t === 'string' || t === 'integer' || t === 'number' || t === 'datetime' || t === 'any'

const epoch = (a: NowArgs) => a.format === 'epoch_ms' || a.format === 'epoch_s'

/** Whether a format suits a field's type, as the server's NOW_FORMAT_TYPE: numbers take an epoch, text a written-out date. */
export function fits(type: FieldType | undefined, a: NowArgs): boolean {
  if (type === 'integer' || type === 'number') return epoch(a)
  if (type === 'string' || type === 'datetime') return !epoch(a)
  return true
}

/** The format a design field's own format or example implies (OC-179); undefined when neither says. */
function designFormat(f: DocField): NowArgs | undefined {
  if (f.format === 'date-time') return { format: 'iso8601' }
  if (f.format === 'date') return { format: 'date' }
  const ex = f.example
  if (typeof ex === 'number' && Number.isInteger(ex)) {
    if (ex > 1e11) return { format: 'epoch_ms' }
    if (ex > 1e8) return { format: 'epoch_s' }
  }
  if (typeof ex === 'string') {
    if (/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}/.test(ex)) return { format: 'iso8601' }
    if (/^\d{4}-\d{2}-\d{2}$/.test(ex)) return { format: 'date' }
  }
  return undefined
}

const pick = (a: Record<string, unknown> | undefined): NowArgs => {
  const out: NowArgs = { format: (a?.format as NowArgs['format']) ?? 'iso8601' }
  if (typeof a?.pattern === 'string' && a.pattern) out.pattern = a.pattern
  if (typeof a?.timezone === 'string' && a.timezone) out.timezone = a.timezone
  return out
}
const key = (a: NowArgs) => `${a.format}|${a.pattern ?? ''}|${a.timezone ?? ''}`

/**
 * The date formats the document's other fields use, in document order: a mapping's
 * formatDatetime or now step, else the design's format or example. The field itself is left out;
 * one entry per distinct format.
 */
export function dateFormats(spec: MappingSpec, fieldId: string): DateFormatRef[] {
  const byField = new Map<string, NowArgs>()
  for (const m of spec.fieldMappings) {
    if (m.state !== 'active' || m.targetFieldId === fieldId) continue
    const st = [...m.transforms].reverse().find((s) => s.op === 'formatDatetime' || s.op === 'now')
    if (st) byField.set(m.targetFieldId, pick(st.args))
  }
  const out: DateFormatRef[] = []
  const seen = new Set<string>()
  const visit = (f: DocField) => {
    if (f.id !== fieldId && f.id !== spec.docSchema.root.id) {
      const args = byField.get(f.id) ?? designFormat(f)
      if (args && !seen.has(key(args))) {
        seen.add(key(args))
        out.push({ fieldId: f.id, name: f.name, args })
      }
    }
    for (const c of f.children ?? []) visit(c)
    if (f.items) visit(f.items)
  }
  visit(spec.docSchema.root)
  return out
}

/**
 * The format a field starts with (OC-179 AC1): the first of the document's date formats that
 * suits it; else the field's own design format or example; else epoch ms for a number, ISO 8601
 * otherwise.
 */
export function defaultNow(spec: MappingSpec, fieldId: string): NowArgs {
  const f = fieldsById(spec).get(fieldId)
  const same = dateFormats(spec, fieldId).find((d) => fits(f?.type, d.args))
  if (same) return { at: 'run', ...same.args }
  const own = f ? designFormat(f) : undefined
  if (own && fits(f?.type, own)) return { at: 'run', ...own }
  return { at: 'run', format: f?.type === 'integer' || f?.type === 'number' ? 'epoch_ms' : 'iso8601' }
}

/** The now step of a field's active mapping, if that's what fills it. */
export function nowOf(spec: MappingSpec, fieldId: string): NowArgs | undefined {
  const m = spec.fieldMappings.find((x) => x.targetFieldId === fieldId && x.state === 'active' && x.sources.length === 0)
  const st = m?.transforms.find((s) => s.op === 'now')
  return st ? { at: st.args?.at === 'write' ? 'write' : 'run', ...pick(st.args) } : undefined
}

/** Fills a field with the current date and time (OC-179), replacing what filled it (a mapping or a not-populated mark). */
export function setNow(spec: MappingSpec, fieldId: string, args: NowArgs): MappingSpec {
  const clean: Record<string, unknown> = { at: args.at ?? 'run', format: args.format ?? 'iso8601' }
  if (args.format === 'custom' && args.pattern) clean.pattern = args.pattern
  if (args.timezone && !epoch(args)) clean.timezone = args.timezone
  const current = spec.fieldMappings.find((m) => m.targetFieldId === fieldId && m.state === 'active' && m.sources.length === 0 && m.transforms.some((s) => s.op === 'now'))
  const step = { op: 'now', args: clean }
  if (current) return { ...spec, fieldMappings: spec.fieldMappings.map((m) => (m.id === current.id ? { ...m, transforms: [step] } : m)) }
  const others = spec.fieldMappings.filter((m) => !(m.targetFieldId === fieldId && m.state !== 'rejected'))
  return { ...spec, fieldMappings: [...others, { id: newMappingId(), targetFieldId: fieldId, sources: [], transforms: [step], origin: 'manual', state: 'active' }] }
}

/** Short label of a format: "epoch ms", "ISO 8601", "yyyy-MM-dd HH:mm". */
export function formatLabel(a: NowArgs): string {
  switch (a.format) {
    case 'epoch_ms':
      return 'epoch ms'
    case 'epoch_s':
      return 'epoch seconds'
    case 'date':
      return 'date'
    case 'custom':
      return a.pattern ?? 'custom'
    default:
      return 'ISO 8601'
  }
}

const MONTHS = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December']
const DAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']

/** The wall-clock parts of an instant in a time zone. */
function parts(d: Date, tz: string) {
  const p = Object.fromEntries(
    new Intl.DateTimeFormat('en-US', { timeZone: tz, hourCycle: 'h23', year: 'numeric', month: 'numeric', day: 'numeric', hour: 'numeric', minute: 'numeric', second: 'numeric', weekday: 'short' })
      .formatToParts(d)
      .map((x) => [x.type, x.value]),
  )
  return { y: +p.year!, mo: +p.month!, d: +p.day!, h: +p.hour! % 24, mi: +p.minute!, s: +p.second!, ms: d.getUTCMilliseconds(), wd: ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'].indexOf(p.weekday!) }
}

const pad = (n: number, w = 2) => String(n).padStart(w, '0')

/** A friendly pattern (yyyy-MM-dd HH:mm, OC-020) applied to wall-clock parts; quoted text is literal. */
function applyPattern(pattern: string, t: ReturnType<typeof parts>): string {
  const tokens: [string, () => string][] = [
    ['yyyy', () => String(t.y)], ['yy', () => pad(t.y % 100)], ['MMMM', () => MONTHS[t.mo - 1]!], ['MMM', () => MONTHS[t.mo - 1]!.slice(0, 3)],
    ['MM', () => pad(t.mo)], ['M', () => String(t.mo)], ['dd', () => pad(t.d)], ['d', () => String(t.d)],
    ['EEEE', () => DAYS[t.wd]!], ['EEE', () => DAYS[t.wd]!.slice(0, 3)], ['HH', () => pad(t.h)], ['hh', () => pad(t.h % 12 || 12)], ['h', () => String(t.h % 12 || 12)],
    ['mm', () => pad(t.mi)], ['m', () => String(t.mi)], ['ss', () => pad(t.s)], ['s', () => String(t.s)], ['SSS', () => pad(t.ms, 3)], ['a', () => (t.h < 12 ? 'AM' : 'PM')],
  ]
  let out = ''
  for (let i = 0; i < pattern.length; ) {
    if (pattern[i] === "'") {
      const end = pattern.indexOf("'", i + 1)
      out += pattern.slice(i + 1, end < 0 ? undefined : end)
      i = end < 0 ? pattern.length : end + 1
      continue
    }
    const tok = tokens.find(([k]) => pattern.startsWith(k, i))
    if (tok) {
      out += tok[1]()
      i += tok[0].length
    } else out += pattern[i++]
  }
  return out
}

/**
 * The value written for an instant (OC-179's live sample), as the server's formatDatetime:
 * epochs are numbers; ISO 8601 is RFC 3339 (Z in UTC, else the zone's offset), its fraction without trailing zeros.
 */
export function formatSample(a: NowArgs, d: Date): string | number {
  const tz = a.timezone || 'UTC'
  switch (a.format) {
    case 'epoch_ms':
      return d.getTime()
    case 'epoch_s':
      return Math.floor(d.getTime() / 1000)
    case 'date':
      return applyPattern('yyyy-MM-dd', parts(d, tz))
    case 'custom':
      return a.pattern ? applyPattern(a.pattern, parts(d, tz)) : ''
  }
  // Go's RFC 3339 with nanoseconds: the fraction without trailing zeros, Z for UTC.
  const t = parts(d, tz)
  const frac = t.ms ? `.${pad(t.ms, 3).replace(/0+$/, '')}` : ''
  const offset = Math.round((Date.UTC(t.y, t.mo - 1, t.d, t.h, t.mi, t.s, t.ms) - d.getTime()) / 60000)
  const zone = offset === 0 ? 'Z' : `${offset < 0 ? '-' : '+'}${pad(Math.floor(Math.abs(offset) / 60))}:${pad(Math.abs(offset) % 60)}`
  return `${applyPattern("yyyy-MM-dd'T'HH:mm:ss", t)}${frac}${zone}`
}

/** In words, as the server's DescribeNow: "the current date and time (when the run started, epoch ms)". */
export function describeNowText(a: NowArgs): string {
  const tz = a.timezone && a.format !== 'epoch_ms' && a.format !== 'epoch_s' ? `, ${a.timezone}` : ''
  return `the current date and time (${a.at === 'write' ? 'when each document is written' : 'when the run started'}, ${formatLabel(a)}${tz})`
}
