import type { DocField, MappingSpec } from '@/features/mapping/types'
import { containersOf, formatPath, parseTemplate, resolve, type RefRoot } from './template'

export type Completion = { label: string; insert: string; detail: string }

const ROOTS: { root: RefRoot; detail: string }[] = [
  { root: 'doc', detail: 'the document' },
  { root: 'self', detail: 'the object holding this field' },
  { root: 'parent', detail: 'the object holding self' },
  { root: 'index', detail: 'position in the array' },
  { root: 'row', detail: 'the source row' },
]

const typeOf = (f: DocField) => (f.type === 'array' ? `array<${f.items?.type ?? '?'}>` : f.type)

/**
 * What can be typed at offset inside a ${…} of a template (OC-138): the names in scope, then a
 * container's fields with their types, or the source row's columns. from is where the
 * completed text starts; null outside a ${…}.
 */
export function completionsAt(spec: MappingSpec, fieldId: string, text: string, offset: number, rowColumns: string[], rootScope: boolean): { from: number; items: Completion[] } | null {
  const open = text.lastIndexOf('${', offset - 1)
  if (open < 0 || text.slice(open, offset).includes('}')) return null
  const typed = text.slice(open + 2, offset).trimStart()
  const typedStart = offset - typed.length
  const ctx = containersOf(spec.docSchema, fieldId)
  if (!ctx) return null
  const dot = typed.lastIndexOf('.')
  if (dot < 0) {
    const items = ROOTS.filter(
      (r) => r.root.startsWith(typed) && (r.root !== 'parent' || ctx.objects.length > 1) && (r.root !== 'index' || ctx.inArray) && (r.root !== 'row' || rootScope),
    ).map((r) => ({ label: r.root, insert: r.root, detail: r.detail }))
    return { from: typedStart, items }
  }
  const head = typed.slice(0, dot)
  const prefix = typed.slice(dot + 1)
  const parsed = parseTemplate('${' + head + '}').refs[0]
  if (!parsed) return { from: offset, items: [] }
  if (parsed.root === 'row') {
    if (!rootScope || parsed.segs.length > 0) return { from: offset, items: [] }
    return { from: typedStart + dot + 1, items: rowColumns.filter((c) => c.startsWith(prefix)).map((c) => ({ label: c, insert: c, detail: 'column' })) }
  }
  if (parsed.root === 'index') return { from: offset, items: [] }
  const base = parsed.root === 'doc' ? spec.docSchema.root : parsed.root === 'self' ? ctx.objects[ctx.objects.length - 1] : ctx.objects[ctx.objects.length - 2]
  const container = base ? resolve(base, parsed.segs) : undefined
  const fields = container?.type === 'array' ? [] : (container?.children ?? [])
  // An identifier goes after the dot; any other name replaces the dot with ["name"].
  const items = fields
    .filter((f) => f.id !== fieldId && f.name.startsWith(prefix))
    .map((f) => ({ label: f.name, insert: formatPath(parsed.root, [f.name]).slice(parsed.root.length), detail: typeOf(f) }))
  return { from: typedStart + dot, items }
}
