import { sha1Hex } from '@/lib/sha1'

// Mirrors backend/internal/mapping/keytemplate.go (renderKey.test.ts runs the shared fixture).

export const MAX_KEY_BYTES = 250

type Filter = { name: 'lower' | 'upper' | 'trim' | 'hash' } | { name: 'pad'; width: number }
export type Segment = { literal: string } | { column: string; filters: Filter[] } | { special: '$table' | '$uuid' }

export class TemplateError extends Error {
  constructor(
    readonly pos: number,
    message: string,
  ) {
    super(`${message} (at position ${pos})`)
  }
}

export function parseKeyTemplate(tpl: string): Segment[] {
  const segs: Segment[] = []
  let lit = ''
  const flush = () => {
    if (lit) segs.push({ literal: lit })
    lit = ''
  }
  for (let i = 0; i < tpl.length; i++) {
    const c = tpl[i]
    if (c === '{' && tpl[i + 1] === '{') {
      lit += '{'
      i++
    } else if (c === '}' && tpl[i + 1] === '}') {
      lit += '}'
      i++
    } else if (c === '}') {
      throw new TemplateError(i, "unexpected '}' (write }} for a literal brace)")
    } else if (c === '{') {
      const end = tpl.indexOf('}', i + 1)
      if (end < 0) throw new TemplateError(i, "unclosed '{'")
      flush()
      segs.push(parseToken(tpl.slice(i + 1, end), i + 1))
      i = end
    } else {
      lit += c
    }
  }
  flush()
  if (segs.length === 0) throw new TemplateError(0, 'the key template is empty')
  return segs
}

function parseToken(body: string, pos: number): Segment {
  const parts = body.split('|')
  const name = parts[0]!.trim()
  if (!name) throw new TemplateError(pos, 'empty token')
  let seg: Segment
  if (name.startsWith('$')) {
    if (name !== '$table' && name !== '$uuid') throw new TemplateError(pos, `unknown special token ${name} (use {$table} or {$uuid})`)
    seg = { special: name }
  } else {
    seg = { column: name, filters: [] }
  }
  let off = pos + parts[0]!.length + 1
  for (const p of parts.slice(1)) {
    const f = p.trim()
    if (!('column' in seg)) throw new TemplateError(off, 'filters only apply to columns')
    if (f === 'lower' || f === 'upper' || f === 'trim' || f === 'hash') seg.filters.push({ name: f })
    else if (f.startsWith('pad:')) {
      const n = Number(f.slice(4))
      if (!Number.isInteger(n) || n < 1 || n > 64) throw new TemplateError(off, 'pad needs a width between 1 and 64, e.g. pad:8')
      seg.filters.push({ name: 'pad', width: n })
    } else throw new TemplateError(off, `unknown filter '${f}' (use lower, upper, trim, pad:N or hash)`)
    off += p.length + 1
  }
  return seg
}

/** Columns referenced by a template, in order, without duplicates. */
export function templateColumns(segs: Segment[]): string[] {
  const out: string[] = []
  for (const s of segs) if ('column' in s && !out.includes(s.column)) out.push(s.column)
  return out
}

function scalar(v: unknown): string {
  if (typeof v === 'number') return Number.isInteger(v) ? String(v) : String(v)
  if (typeof v === 'boolean') return v ? 'true' : 'false'
  return String(v)
}

export type RenderResult = { key: string } | { error: 'KEY_NULL_COMPONENT' | 'KEY_TOO_LONG'; message: string }

/** Renders a key from a row of encoded values (column → value). */
export function renderKey(segs: Segment[], row: Record<string, unknown>, table = ''): RenderResult {
  let out = ''
  for (const s of segs) {
    if ('literal' in s) out += s.literal
    else if ('special' in s) out += s.special === '$table' ? table : '{uuid}'
    else {
      const v = row[s.column]
      if (v === null || v === undefined) return { error: 'KEY_NULL_COMPONENT', message: `key column ${s.column} is null` }
      let str = scalar(v)
      for (const f of s.filters) {
        if (f.name === 'lower') str = str.toLowerCase()
        else if (f.name === 'upper') str = str.toUpperCase()
        else if (f.name === 'trim') str = str.trim()
        else if (f.name === 'hash') str = sha1Hex(str)
        else if (f.name === 'pad' && str.length < f.width) str = '0'.repeat(f.width - str.length) + str
      }
      out += str
    }
  }
  const bytes = new TextEncoder().encode(out).length
  if (bytes > MAX_KEY_BYTES) return { error: 'KEY_TOO_LONG', message: `the key is ${bytes} bytes; Couchbase allows ${MAX_KEY_BYTES}` }
  return { key: out }
}
