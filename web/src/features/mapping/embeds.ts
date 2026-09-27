import { leaves, type Leaf } from '@/features/docschema/docschemaOps'
import type { CanvasColumn, DocField, Embed, EmbedTable, MappingSpec, SourceRef } from './types'

// Embeds (OC-021): a related table nested as an object (many-to-one) or an array of objects
// (one-to-many). Mirrors internal/mapping/embeds.go.

/** The key of a mapping source: the column, or "embedId:column" for an embedded column. */
export const srcKey = (s: Pick<SourceRef, 'column' | 'embed'>) => (s.embed ? `${s.embed}:${s.column}` : s.column)

function find(f: DocField, id: string): DocField | undefined {
  if (f.id === id) return f
  for (const c of f.children ?? []) {
    const r = find(c, id)
    if (r) return r
  }
  return f.items ? find(f.items, id) : undefined
}

/** The object holding an embed's fields: the target object, or the target array's item object. */
export function embedContainer(spec: MappingSpec, e: Embed): DocField | undefined {
  const f = find(spec.docSchema.root, e.targetFieldId)
  if (!f) return undefined
  if (e.kind === 'many-to-one') return f.type === 'object' ? f : undefined
  return f.type === 'array' && f.items?.type === 'object' ? f.items : undefined
}

/** field id → embed id, for every field inside an embed. */
export function embedFieldIds(spec: MappingSpec): Map<string, string> {
  const out = new Map<string, string>()
  for (const e of spec.embeds) {
    const c = embedContainer(spec, e)
    if (!c) continue
    const visit = (f: DocField) => {
      for (const k of f.children ?? []) {
        out.set(k.id, e.id)
        visit(k)
      }
      if (f.items && f !== c) {
        out.set(f.items.id, e.id)
        visit(f.items)
      }
    }
    visit(c)
  }
  return out
}

/** The canvas rows of the embedded tables, in embed order (join columns last and muted by the UI). */
export function embedColumns(spec: MappingSpec, tables: Record<string, EmbedTable> | undefined): CanvasColumn[] {
  const out: CanvasColumn[] = []
  for (const e of spec.embeds) {
    for (const c of tables?.[e.id]?.columns ?? []) out.push({ ...c, name: srcKey({ column: c.name, embed: e.id }), embed: e.id, column: c.name })
  }
  return out
}

/** Every designed field by id (arrays' item fields included), for type lookups. */
export function fieldsById(spec: MappingSpec): Map<string, DocField> {
  const out = new Map<string, DocField>()
  const visit = (f: DocField) => {
    out.set(f.id, f)
    for (const c of f.children ?? []) visit(c)
    if (f.items) visit(f.items)
  }
  visit(spec.docSchema.root)
  return out
}

/** Leaves inside embeds: mappable fields that aren't containers. */
export function embedLeafIds(spec: MappingSpec): string[] {
  const byId = fieldsById(spec)
  return [...embedFieldIds(spec).keys()].filter((id) => {
    const f = byId.get(id)
    return !!f && f.name !== '[]' && !(f.type === 'object' && (f.children?.length ?? 0) > 0)
  })
}

/** Removes an embed with its field and its mappings. */
export function removeEmbed(spec: MappingSpec, id: string): MappingSpec {
  const e = spec.embeds.find((x) => x.id === id)
  if (!e) return spec
  const drop = (f: DocField): DocField => ({ ...f, children: f.children?.filter((c) => c.id !== e.targetFieldId).map(drop) })
  return {
    ...spec,
    embeds: spec.embeds.filter((x) => x.id !== id),
    docSchema: { ...spec.docSchema, root: drop(spec.docSchema.root), source: 'manual' },
    fieldMappings: spec.fieldMappings.filter((m) => !m.sources.some((s) => s.embed === id)),
  }
}

/**
 * The canvas's mappable fields: the design's leaves plus the fields inside arrays of objects
 * (an embed's items, e.g. orders[].total), which the server's leaves leave out.
 */
export function canvasLeaves(spec: MappingSpec): Leaf[] {
  const out = leaves(spec.docSchema)
  const visitItems = (f: DocField, path: string, parents: string[]) => {
    for (const c of f.children ?? []) {
      const p = path ? `${path}.${c.name}` : c.name
      if (c.type === 'array' && c.items?.type === 'object') {
        const sub = { ...spec, docSchema: { ...spec.docSchema, root: { ...c.items, id: spec.docSchema.root.id } } }
        for (const l of leaves(sub.docSchema)) out.push({ ...l, path: `${p}[].${l.path}`, parentNames: [...parents, c.name, ...l.parentNames] })
        visitItems(c.items, `${p}[]`, [...parents, c.name])
      } else if (c.type === 'object') visitItems(c, p, [...parents, c.name])
    }
  }
  visitItems(spec.docSchema.root, '', [])
  return out
}
