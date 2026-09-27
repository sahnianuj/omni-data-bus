import { parseKeyTemplate, templateColumns, TemplateError, type Segment } from './renderKey'
import type { SourceColumn } from './types'

// Mirrors mapping.AnalyzeKey (OC-016 AC3) for the badge; the server's
// validation issues stay authoritative.

export type KeyVerdict = 'unique' | 'may-collide' | 'not-idempotent'

export type KeyAnalysis =
  | { ok: true; segments: Segment[]; verdict: KeyVerdict; unknown: string[] }
  | { ok: false; error: string; pos: number }

export function analyzeKey(template: string, columns: SourceColumn[]): KeyAnalysis {
  let segments: Segment[]
  try {
    segments = parseKeyTemplate(template)
  } catch (e) {
    if (e instanceof TemplateError) return { ok: false, error: e.message, pos: e.pos }
    throw e
  }
  const used = new Set(templateColumns(segments))
  const known = new Set(columns.map((c) => c.name))
  const unknown = [...used].filter((c) => !known.has(c))
  if (segments.some((s) => 'special' in s && s.special === '$uuid')) return { ok: true, segments, verdict: 'not-idempotent', unknown }
  const pk = columns.filter((c) => c.isPrimaryKey).map((c) => c.name)
  const coversPk = pk.length > 0 && pk.every((c) => used.has(c))
  const coversUnique = columns.some((c) => c.isUnique && used.has(c.name))
  return { ok: true, segments, verdict: coversPk || coversUnique ? 'unique' : 'may-collide', unknown }
}

/** The `{`-token being typed at the caret, if any: the text after the last unclosed `{`. */
export function tokenAtCaret(value: string, caret: number): { start: number; query: string } | null {
  const before = value.slice(0, caret)
  const open = before.lastIndexOf('{')
  if (open < 0 || before.lastIndexOf('}') > open || before[open - 1] === '{') return null
  const query = before.slice(open + 1)
  if (query.includes('|')) return null
  return { start: open, query }
}

/** Inserts `{col}` over the partial token, returning the new value and caret. */
export function insertToken(value: string, caret: number, start: number, column: string): { value: string; caret: number } {
  const after = value.slice(caret)
  const rest = after.startsWith('}') ? after.slice(1) : after
  const token = `{${column}}`
  return { value: value.slice(0, start) + token + rest, caret: start + token.length }
}
