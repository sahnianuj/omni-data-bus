import { z } from 'zod'

// Argument schemas per transform op (OC-020). Mirrors the Go compilers in
// backend/internal/transform; opSchemas.test.ts runs the shared fixture testdata/op-args.json.

const PATTERN_LETTERS = /^(?:yyyy|yy|MMMM|MMM|MM|M|dd|d|EEEE|EEE|HH|hh|h|mm|m|ss|s|SSSSSS|SSS|a|XXX|xxx|Z)/

/** Whether a friendly date pattern only uses known letters (quoted text is literal). */
export function validPattern(p: string): boolean {
  for (let i = 0; i < p.length; ) {
    const c = p[i]!
    if (c === "'") {
      const end = p.indexOf("'", i + 1)
      if (end < 0) return false
      i = end + 1
      continue
    }
    const m = PATTERN_LETTERS.exec(p.slice(i))
    if (m) {
      i += m[0].length
      continue
    }
    if (/[A-Za-z]/.test(c)) return false
    i++
  }
  return true
}

function validTimezone(tz: string): boolean {
  try {
    new Intl.DateTimeFormat('en', { timeZone: tz })
    return true
  } catch {
    return false
  }
}

function validRegex(p: string): boolean {
  try {
    new RegExp(p)
    return true
  } catch {
    return false
  }
}

const tz = z.string().refine(validTimezone, 'Unknown timezone').optional()
const anyValue = z.custom<unknown>((v) => v !== undefined, 'A value is required')

export const opSchemas = {
  toString: z.object({}),
  parseInteger: z.object({ strict: z.boolean().optional() }),
  parseNumber: z.object({ strict: z.boolean().optional() }),
  parseBoolean: z.object({ strict: z.boolean().optional() }),
  toDatetime: z.object({ inputFormat: z.string().refine(validPattern, 'Unknown pattern letter').optional(), timezone: tz, strict: z.boolean().optional() }),
  formatDatetime: z
    .object({ format: z.enum(['iso8601', 'date', 'epoch_ms', 'epoch_s', 'custom']).optional(), pattern: z.string().optional(), timezone: tz })
    .refine((a) => a.format !== 'custom' || (!!a.pattern && validPattern(a.pattern)), { message: 'A custom format needs a valid pattern', path: ['pattern'] }),
  trim: z.object({}),
  lower: z.object({}),
  upper: z.object({}),
  substring: z.object({ start: z.number().int().min(0).optional(), length: z.number().int().optional() }),
  replace: z
    .object({ pattern: z.string().min(1, 'Enter the text to replace'), replacement: z.string().optional(), regex: z.boolean().optional() })
    .refine((a) => !a.regex || validRegex(a.pattern), { message: 'Invalid regular expression', path: ['pattern'] }),
  concat: z.object({ separator: z.string().optional(), skipNulls: z.boolean().optional() }),
  split: z.object({ separator: z.string().min(1, 'Enter a separator'), trim: z.boolean().optional() }),
  default: z.object({ value: anyValue }),
  nullIf: z.object({ value: anyValue }),
  constant: z.object({ value: anyValue }),
  lookup: z.object({
    map: z.record(z.string(), z.unknown()).refine((m) => Object.keys(m).length > 0, 'Add at least one entry'),
    fallback: z.enum(['keep', 'null', 'value']).optional(),
    value: z.unknown().optional(),
  }),
  round: z.object({ digits: z.number().int('Whole number').min(0).max(15).optional() }),
  jsonParse: z.object({}),
  mask: z.object({ keepLast: z.number().int().min(0).optional(), char: z.string().optional() }),
  hash: z.object({ algo: z.enum(['sha256', 'sha1']).optional() }),
  expression: z.object({ expr: z.string().trim().min(1, 'Enter an expression') }),
} as const

export type OpName = keyof typeof opSchemas

export const opCategories: { id: string; ops: OpName[] }[] = [
  { id: 'convert', ops: ['toString', 'parseInteger', 'parseNumber', 'parseBoolean'] },
  { id: 'text', ops: ['trim', 'lower', 'upper', 'substring', 'replace', 'concat', 'split'] },
  { id: 'datetime', ops: ['toDatetime', 'formatDatetime'] },
  { id: 'values', ops: ['default', 'nullIf', 'constant', 'lookup', 'round', 'jsonParse'] },
  { id: 'privacy', ops: ['mask', 'hash'] },
  { id: 'advanced', ops: ['expression'] },
]

/** Validates an op's args; returns field → message. */
export function validateArgs(op: string, args: Record<string, unknown> | undefined): Record<string, string> {
  const schema = opSchemas[op as OpName]
  if (!schema) return { op: `Unknown transform '${op}'` }
  const r = schema.safeParse(args ?? {})
  if (r.success) return {}
  return Object.fromEntries(r.error.issues.map((i) => [String(i.path[0] ?? 'args'), i.message]))
}
