import type { FieldType } from './types'

export type CompatClass = 'exact' | 'lossless' | 'lossy' | 'incompatible'
export type Compat = { class: CompatClass; transform?: { op: string; args?: Record<string, unknown> } }

// Source jsonType → target type. Mirrors backend/internal/mapping/compat.json; canConnect.test.ts
// checks every pair against that file.
const T = ['string', 'integer', 'number', 'boolean', 'datetime', 'any', 'object', 'array'] as const
const inc: Compat = { class: 'incompatible' }
const exact: Compat = { class: 'exact' }
const row = (over: Partial<Record<(typeof T)[number], Compat>>, rest: Compat = inc) =>
  Object.fromEntries(T.map((t) => [t, over[t] ?? rest])) as Record<string, Compat>
const lossy = (op?: string, args?: Record<string, unknown>): Compat => ({ class: 'lossy', ...(op ? { transform: { op, ...(args ? { args } : {}) } } : {}) })
const toString: Compat = { class: 'lossless', transform: { op: 'toString' } }

export const compatMatrix: Record<string, Record<string, Compat>> = {
  string: row({ string: exact, integer: lossy('parseInteger'), number: lossy('parseNumber'), boolean: lossy('parseBoolean'), datetime: lossy('toDatetime'), any: exact }),
  integer: row({ string: toString, integer: exact, number: exact, boolean: lossy('parseBoolean'), datetime: lossy('toDatetime'), any: exact }),
  number: row({ string: toString, integer: lossy('parseInteger'), number: exact, datetime: lossy('toDatetime'), any: exact }),
  boolean: row({ string: toString, boolean: exact, any: exact }),
  datetime: row({ string: toString, integer: lossy('formatDatetime', { format: 'epoch_ms' }), number: lossy('formatDatetime', { format: 'epoch_ms' }), datetime: exact, any: exact }),
  any: row({ string: toString, any: exact, object: exact, array: exact }, lossy()),
  object: row({ object: exact, any: exact }),
  array: row({ array: exact, any: exact }),
}

export type CanConnect = { allowed: boolean; class: CompatClass; suggestedTransform?: Compat['transform'] }

/** Whether a source column (jsonType) can feed a target field, and the conversion to add. */
export function canConnect(sourceType: string, target: FieldType): CanConnect {
  const c = compatMatrix[sourceType]?.[target] ?? inc
  return { allowed: c.class !== 'incompatible', class: c.class, suggestedTransform: c.transform }
}
