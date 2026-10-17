import type { DocField, DocSchema, MappingSpec } from '@/features/mapping/types'
import { containersOf, formatPath, parseTemplate, resolve, type RefRoot, type Seg } from './template'

// Computed fields follow the document (OC-139): their templates name fields by path, so a
// rename or a move rewrites every reference to the field, in the same undo step. A reference to
// a removed field is left as written: validation flags it (COMPUTED_UNKNOWN_FIELD) instead of
// the value silently turning empty.

/** A field's chain of fields from the root (array items included), or undefined. */
function chainTo(schema: DocSchema, id: string): DocField[] | undefined {
  const walk = (node: DocField, chain: DocField[]): DocField[] | undefined => {
    for (const c of node.children ?? []) {
      if (c.id === id) return [...chain, c]
      const inner = walk(c, [...chain, c])
      if (inner) return inner
    }
    if (node.items) {
      if (node.items.id === id) return [...chain, node.items]
      return walk(node.items, [...chain, node.items])
    }
    return undefined
  }
  return walk(schema.root, [])
}

/**
 * The path from a container to a field below it, with array positions kept from the original
 * reference (an item reached through an array needs its index); undefined when the field isn't
 * below the container or an index is missing.
 */
function pathFrom(schema: DocSchema, from: DocField, targetId: string, indexes: number[]): Seg[] | undefined {
  const fromChain = from.id === schema.root.id ? [] : chainTo(schema, from.id)
  const chain = chainTo(schema, targetId)
  if (!chain || !fromChain || fromChain.some((f, i) => chain[i]?.id !== f.id)) return undefined
  const segs: Seg[] = []
  const idx = [...indexes]
  for (const f of chain.slice(fromChain.length)) {
    if (f.name === '[]') {
      const n = idx.shift()
      if (n === undefined) return undefined
      segs.push(n)
    } else segs.push(f.name)
  }
  return segs
}

/** What a reference reads in a design: the field and the array positions on the way. */
function target(schema: DocSchema, fieldId: string, root: RefRoot, segs: Seg[]): { id: string; indexes: number[] } | undefined {
  const ctx = containersOf(schema, fieldId)
  if (!ctx) return undefined
  const base = root === 'doc' ? schema.root : root === 'self' ? ctx.objects[ctx.objects.length - 1] : root === 'parent' ? ctx.objects[ctx.objects.length - 2] : undefined
  const f = base ? resolve(base, segs) : undefined
  return f ? { id: f.id, indexes: segs.filter((s): s is number => typeof s === 'number') } : undefined
}

/** The reference written for the new design: the same root when it still fits, else doc. */
function rewrite(schema: DocSchema, fieldId: string, root: RefRoot, t: { id: string; indexes: number[] }): string | undefined {
  const ctx = containersOf(schema, fieldId)
  if (!ctx) return undefined
  const bases: [RefRoot, DocField | undefined][] = [
    [root, root === 'doc' ? schema.root : root === 'self' ? ctx.objects[ctx.objects.length - 1] : ctx.objects[ctx.objects.length - 2]],
    ['doc', schema.root],
  ]
  for (const [r, base] of bases) {
    const segs = base ? pathFrom(schema, base, t.id, t.indexes) : undefined
    if (segs && segs.length > 0) return formatPath(r, segs)
  }
  return undefined
}

/**
 * Rewrites the templates of next's computed fields after a design change from prev: each
 * reference that read a field still in the design reads it again, wherever it moved or however
 * it's named. Returns next unchanged when nothing needs rewriting.
 */
export function followDesign(prev: MappingSpec, next: MappingSpec): MappingSpec {
  if (prev.docSchema === next.docSchema || !next.fieldMappings.some((m) => m.computed)) return next
  let changed = false
  const fieldMappings = next.fieldMappings.map((m) => {
    if (!m.computed || m.state !== 'active') return m
    const { refs, error } = parseTemplate(m.computed.expr)
    if (error) return m
    let expr = m.computed.expr
    for (const r of [...refs].reverse()) {
      if (r.root === 'index' || r.root === 'row') continue
      const before = target(prev.docSchema, m.targetFieldId, r.root, r.segs)
      if (!before) continue // unknown before the change: not ours to fix
      const now = target(next.docSchema, m.targetFieldId, r.root, r.segs)
      if (now?.id === before.id) continue // still reads the same field
      const written = rewrite(next.docSchema, m.targetFieldId, r.root, before)
      if (!written) continue // removed, or no path fits: validation flags it
      expr = expr.slice(0, r.start) + written + expr.slice(r.end)
    }
    if (expr === m.computed.expr) return m
    changed = true
    return { ...m, computed: { ...m.computed, expr } }
  })
  return changed ? { ...next, fieldMappings } : next
}

/** The fields a computed field reads, and the computed fields that read it (OC-139). */
export function usesAndUsedBy(spec: MappingSpec, fieldId: string): { uses: string[]; usedBy: string[] } {
  const reads = (m: { targetFieldId: string; computed?: { expr: string } }) =>
    parseTemplate(m.computed?.expr ?? '').refs.flatMap((r) => (r.root === 'index' || r.root === 'row' ? [] : [target(spec.docSchema, m.targetFieldId, r.root, r.segs)?.id]))
  const own = spec.fieldMappings.find((m) => m.targetFieldId === fieldId && m.state === 'active' && m.computed)
  const uses = own ? [...new Set(reads(own).filter((x): x is string => !!x))] : []
  const usedBy = spec.fieldMappings.filter((m) => m.state === 'active' && m.computed && m.targetFieldId !== fieldId && reads(m).includes(fieldId)).map((m) => m.targetFieldId)
  return { uses, usedBy }
}
