import type { DocField, DocSchema } from '@/features/mapping/types'

// Template computed fields (OC-136) on the client: the same grammar as the server's
// transform.ParseTemplate, for autocomplete (OC-138) and for keeping references up to date
// when fields are renamed, moved or removed (OC-139).

export type RefRoot = 'doc' | 'self' | 'parent' | 'index' | 'row'
export type Seg = string | number

/** One ${…} substitution: its root, path, and where the path sits in the text. */
export type TemplateRef = { root: RefRoot; segs: Seg[]; start: number; end: number }

const ROOT = /^(doc|self|parent|index|row)/
const SEG = /^(?:\.([A-Za-z_$][A-Za-z0-9_$]*)|\[(\d+)\]|\["((?:[^"\\]|\\.)*)"\])/

/** The ${…} references of a template, or the first syntax error (with its offset). */
export function parseTemplate(expr: string): { refs: TemplateRef[]; error?: { offset: number; message: string } } {
  const refs: TemplateRef[] = []
  let i = 0
  while (i < expr.length) {
    if (expr.startsWith('\\${', i)) {
      i += 3
      continue
    }
    if (!expr.startsWith('${', i)) {
      i++
      continue
    }
    const close = expr.indexOf('}', i + 2)
    if (close < 0) return { refs, error: { offset: i + 2, message: '${ isn’t closed with }' } }
    const body = expr.slice(i + 2, close)
    const lead = body.length - body.trimStart().length
    const text = body.trim()
    const start = i + 2 + lead
    const m = ROOT.exec(text)
    if (!m) return { refs, error: { offset: start, message: 'A value starts with doc, self, parent, row or index' } }
    const ref: TemplateRef = { root: m[1] as RefRoot, segs: [], start, end: start + text.length }
    let rest = text.slice(m[0].length)
    while (rest) {
      const g = SEG.exec(rest)
      if (!g) return { refs, error: { offset: start, message: 'A template only reads values, e.g. ${doc.firstName}' } }
      ref.segs.push(g[1] ?? (g[2] !== undefined ? Number(g[2]) : JSON.parse(`"${g[3]}"`)))
      rest = rest.slice(g[0].length)
    }
    refs.push(ref)
    i = close + 1
  }
  return { refs }
}

/** Writes a path back: `.name`, `[0]`, or `["odd name"]`. */
export function formatPath(root: RefRoot, segs: Seg[]): string {
  return root + segs.map((s) => (typeof s === 'number' ? `[${s}]` : /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(s) ? `.${s}` : `[${JSON.stringify(s)}]`)).join('')
}

/** The objects from the root down to the one holding a field (array items count as objects). */
export function containersOf(schema: DocSchema, fieldId: string): { objects: DocField[]; inArray: boolean } | undefined {
  const find = (node: DocField, chain: DocField[], inArray: boolean): { objects: DocField[]; inArray: boolean } | undefined => {
    for (const c of node.children ?? []) {
      if (c.id === fieldId) return { objects: chain, inArray }
      if (c.type === 'object') {
        const r = find(c, [...chain, c], inArray)
        if (r) return r
      }
      if (c.type === 'array' && c.items) {
        if (c.items.id === fieldId) return { objects: chain, inArray }
        if (c.items.type === 'object') {
          const r = find(c.items, [...chain, c.items], true)
          if (r) return r
        }
      }
    }
    return undefined
  }
  return find(schema.root, [schema.root], false)
}

/** The design field a path leads to from a container, if any. */
export function resolve(from: DocField, segs: Seg[]): DocField | undefined {
  let cur: DocField | undefined = from
  for (const s of segs) {
    if (!cur) return undefined
    if (typeof s === 'number') cur = cur.type === 'array' ? cur.items : undefined
    else cur = (cur.children ?? []).find((c) => c.name === s)
  }
  return cur
}

/**
 * A template written like code, "paid_in_out::" + ${doc.id}, as the template it means:
 * paid_in_out::${doc.id}. The text is the value as written, so quotes and + would end up in it.
 * null when the text isn't quoted parts joined with +.
 */
export function asTemplate(text: string): string | null {
  const parts: string[] = []
  let start = 0
  let quote = ''
  let inRef = false
  for (let i = 0; i < text.length; i++) {
    const c = text[i]!
    if (inRef) inRef = c !== '}'
    else if (quote) {
      if (c === '\\') i++
      else if (c === quote) quote = ''
    } else if (c === '"' || c === "'") quote = c
    else if (text.startsWith('${', i)) inRef = true
    else if (c === '+') {
      parts.push(text.slice(start, i))
      start = i + 1
    }
  }
  parts.push(text.slice(start))
  if (parts.length < 2) return null
  let quoted = false
  const out: string[] = []
  for (const raw of parts.map((p) => p.trim())) {
    if (/^"(?:[^"\\]|\\.)*"$/.test(raw)) {
      out.push(JSON.parse(raw) as string)
      quoted = true
    } else if (/^'[^']*'$/.test(raw)) {
      out.push(raw.slice(1, -1))
      quoted = true
    } else if (/^\$\{[^}]*\}$/.test(raw)) out.push(raw)
    else if (ROOT.test(raw) && !parseTemplate('${' + raw + '}').error) out.push('${' + raw + '}')
    else return null
  }
  return quoted ? out.join('') : null
}
