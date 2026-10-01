import { leaves, pathsById, type Leaf } from '@/features/docschema/docschemaOps'
import type { CanvasColumn, DocField, Embed, EmbedTable, MappingSpec, SourceRef } from './types'

// Relations (OC-021 embeds; spec v2 relation tree from OC-037): a related table nested as an
// object (many-to-one), an array of objects, values or a keyed object (one-to-many and
// many-to-many, OC-043 – OC-045), or no field at all (lookup-only). Relations nest through
// parentId. Mirrors internal/mapping/embeds.go and scope.go.

/**
 * The key of a mapping source: the column, "relationId:column" for a relation's column, or
 * "relationId:via:column" for a many-to-many junction column (OC-044).
 */
export const srcKey = (s: Pick<SourceRef, 'column' | 'relation'> & { from?: string }) =>
  s.relation ? (s.from === 'via' ? `${s.relation}:via:${s.column}` : `${s.relation}:${s.column}`) : s.column

function find(f: DocField, id: string): DocField | undefined {
  if (f.id === id) return f
  for (const c of f.children ?? []) {
    const r = find(c, id)
    if (r) return r
  }
  return f.items ? find(f.items, id) : undefined
}

const shapeOf = (e: Embed) => e.shape ?? 'objects'

/**
 * The object holding a relation's fields: the target object (many-to-one, keyed), or the target
 * array's item object. A composer adapter (OC-047) lets a many relation fill an object (first)
 * and a many-to-one relation fill an array's item object (wrap).
 */
export function embedContainer(spec: MappingSpec, e: Embed): DocField | undefined {
  if (e.lookupOnly || !e.targetFieldId) return undefined
  const f = find(spec.docSchema.root, e.targetFieldId)
  if (!f) return undefined
  const many = e.kind !== 'many-to-one'
  switch (f.binding?.adapter) {
    case 'first':
      return many && f.type === 'object' ? f : undefined
    case 'wrap':
      return !many && f.type === 'array' && f.items?.type === 'object' ? f.items : undefined
  }
  if (e.kind === 'many-to-one') return f.type === 'object' ? f : undefined
  if (shapeOf(e) === 'keyed') return f.type === 'object' ? f : undefined
  if (shapeOf(e) === 'scalars') return undefined
  return f.type === 'array' && f.items?.type === 'object' ? f.items : undefined
}

/** field id → the relation whose rows fill it ('' = the root row), nearest relation first (OC-043). */
export function fieldScopes(spec: MappingSpec): Map<string, string> {
  const byTarget = new Map(spec.relations.filter((r) => !r.lookupOnly && r.targetFieldId).map((r) => [r.targetFieldId!, r.id]))
  const out = new Map<string, string>()
  const walk = (f: DocField, scope: string) => {
    for (const c of f.children ?? []) {
      out.set(c.id, scope)
      const inner = byTarget.get(c.id) ?? scope
      walk(c, inner)
      if (c.items) {
        out.set(c.items.id, inner)
        walk(c.items, inner)
      }
    }
  }
  walk(spec.docSchema.root, '')
  return out
}

/** The relations a field in scope can read, nearest first (the scope's relation and its ancestors). */
export function scopeChain(spec: MappingSpec, scope: string): string[] {
  const out: string[] = []
  for (let id = scope; id && !out.includes(id); ) {
    out.push(id)
    id = spec.relations.find((r) => r.id === id)?.parentId ?? ''
  }
  return out
}

/** Whether a field in scope may read relation rel's columns: the root, a relation on its path, or a lookup attached to that path. */
export function inScope(spec: MappingSpec, scope: string, rel: string | undefined): boolean {
  if (!rel) return true
  const chain = scopeChain(spec, scope)
  if (chain.includes(rel)) return true
  const r = spec.relations.find((x) => x.id === rel)
  if (!r?.lookupOnly) return false
  return !r.parentId || chain.includes(r.parentId)
}

/** field id → relation id, for every field inside a relation (containers left out). */
export function embedFieldIds(spec: MappingSpec): Map<string, string> {
  const containers = new Set(spec.relations.map((e) => embedContainer(spec, e)?.id).filter(Boolean))
  const out = new Map<string, string>()
  for (const [id, scope] of fieldScopes(spec)) if (scope && !containers.has(id)) out.set(id, scope)
  return out
}

/**
 * The canvas rows of the relations' tables, in relation order: their columns, then a
 * many-to-many relation's junction payload columns ("relId:via:column").
 */
export function embedColumns(spec: MappingSpec, tables: Record<string, EmbedTable> | undefined): CanvasColumn[] {
  const out: CanvasColumn[] = []
  for (const e of spec.relations) {
    const t = tables?.[e.id]
    for (const c of t?.columns ?? []) out.push({ ...c, name: srcKey({ column: c.name, relation: e.id }), embed: e.id, column: c.name })
    for (const c of t?.via ?? []) out.push({ ...c, name: srcKey({ column: c.name, relation: e.id, from: 'via' }), embed: e.id, column: c.name, from: 'via' })
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

/** Leaves inside relations: mappable fields that aren't containers. */
export function embedLeafIds(spec: MappingSpec): string[] {
  const byId = fieldsById(spec)
  return [...embedFieldIds(spec).keys()].filter((id) => {
    const f = byId.get(id)
    return !!f && f.name !== '[]' && !(f.type === 'object' && (f.children?.length ?? 0) > 0)
  })
}

/** The relation and every relation nested under it. */
export function withDescendants(spec: MappingSpec, id: string): Set<string> {
  const out = new Set([id])
  for (let grew = true; grew; ) {
    grew = false
    for (const r of spec.relations) {
      if (r.parentId && out.has(r.parentId) && !out.has(r.id)) {
        out.add(r.id)
        grew = true
      }
    }
  }
  return out
}

/**
 * Removes a relation with its nested relations, its field, their mappings and aggregates, and
 * the fields only their columns fed: a lookup's fields sit beside its foreign key, not in a
 * field of its own (OC-069). A field the imported design defines stays, unmapped.
 */
export function removeEmbed(spec: MappingSpec, id: string): MappingSpec {
  if (!spec.relations.some((x) => x.id === id)) return spec
  const gone = withDescendants(spec, id)
  const fields = new Set(spec.relations.filter((r) => gone.has(r.id)).map((r) => r.targetFieldId).filter(Boolean))
  const onlyGone = new Map<string, boolean>()
  for (const m of spec.fieldMappings) {
    const fed = m.sources.length > 0 && m.sources.every((s) => !!s.relation && gone.has(s.relation))
    onlyGone.set(m.targetFieldId, (onlyGone.get(m.targetFieldId) ?? true) && fed)
  }
  const contract = new Set((spec.docSchema.imported?.fields ?? []).map((f) => f.path))
  const paths = contract.size > 0 ? pathsById(spec.docSchema) : undefined
  for (const [field, only] of onlyGone) if (only && !contract.has(paths?.get(field) ?? '')) fields.add(field)
  const drop = (f: DocField): DocField => ({
    ...f,
    children: f.children?.filter((c) => !fields.has(c.id)).map(drop),
    ...(f.items ? { items: drop(f.items) } : {}),
  })
  const aggFields = new Set((spec.aggregates ?? []).filter((a) => gone.has(a.relationId)).map((a) => a.targetFieldId))
  const dropAgg = (f: DocField): DocField => ({ ...f, children: f.children?.filter((c) => !aggFields.has(c.id)).map(dropAgg), ...(f.items ? { items: dropAgg(f.items) } : {}) })
  return {
    ...spec,
    relations: spec.relations.filter((x) => !gone.has(x.id)),
    aggregates: (spec.aggregates ?? []).filter((a) => !gone.has(a.relationId)),
    docSchema: { ...spec.docSchema, root: dropAgg(drop(spec.docSchema.root)), source: 'manual' },
    fieldMappings: spec.fieldMappings.filter((m) => !m.sources.some((s) => s.relation && gone.has(s.relation))),
  }
}

/**
 * The canvas's mappable fields: the design's leaves plus the fields inside arrays of objects
 * (a relation's items, e.g. orders[].total), which the server's leaves leave out.
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
