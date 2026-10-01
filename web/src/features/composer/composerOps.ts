import { addField, findField, move, newFieldId, OpError, remove, rename, setType, uniqueName } from '@/features/docschema/docschemaOps'
import { fieldTypeOf } from '@/features/docschema/generate'
import { withDescendants } from '@/features/mapping/embeds'
import { newMappingId, setConstant } from '@/features/mapping/mappingOps'
import { rename as renameStyle } from '@/lib/naming'
import type { DocField, DocSchema, FieldType, MappingSpec, Relation, SourceColumn } from '@/features/mapping/types'
import { boundRelations, fieldScopes } from './scope'

// Pure edits of a composed document (OC-048): the design tree, bindings (a relation whose
// targetFieldId is the node, OC-047), lookups and the driving table. Each returns a new spec;
// the workbench store records them for undo and autosave.

export { OpError }

export type NodeKind = 'field' | 'object' | 'array'
export type TableRef = { schema: string; name: string }
export type Adapter = 'first' | 'wrap'

const REL_ALPHABET = '0123456789abcdefghijklmnopqrstuvwxyz'

export function newRelationId(): string {
  const bytes = new Uint8Array(8)
  crypto.getRandomValues(bytes)
  return 'rel_' + Array.from(bytes, (b) => REL_ALPHABET[b % REL_ALPHABET.length]).join('')
}

const withSchema = (spec: MappingSpec, docSchema: DocSchema): MappingSpec => ({ ...spec, docSchema: { ...docSchema, source: 'manual' } })

/** The field with id (the root included). */
export function nodeOf(spec: MappingSpec, id: string): DocField | undefined {
  if (id === spec.docSchema.root.id) return spec.docSchema.root
  return findField(spec.docSchema, id)?.field
}

/** The object that holds a node's children: the node itself, or an array's item object. */
export function containerOf(spec: MappingSpec, id: string): DocField | undefined {
  const n = nodeOf(spec, id)
  if (!n) return undefined
  if (n.type === 'object') return n
  if (n.type === 'array' && n.items?.type === 'object') return n.items
  return undefined
}

function updateNode(schema: DocSchema, id: string, fn: (f: DocField) => DocField): DocSchema {
  const visit = (f: DocField): DocField => {
    if (f.id === id) return fn(f)
    let changed = false
    const children = f.children?.map((c) => {
      const n = visit(c)
      changed ||= n !== c
      return n
    })
    const items = f.items ? visit(f.items) : undefined
    changed ||= items !== f.items
    return changed ? { ...f, children, ...(f.items ? { items } : {}) } : f
  }
  return { ...schema, root: visit(schema.root) }
}

/**
 * Adds a field, object or array under a node (an array gets it in its item object, which
 * becomes an object if it held values), at index. Arrays start as arrays of objects.
 */
export function addNode(spec: MappingSpec, parentId: string, kind: NodeKind, name?: string, index?: number): { spec: MappingSpec; id: string } {
  let schema = spec.docSchema
  let target = parentId
  const parent = nodeOf(spec, parentId)
  if (!parent) throw new OpError('Unknown node.')
  if (parent.type === 'array') {
    if (parent.items?.type !== 'object') {
      const items: DocField = { id: parent.items?.id ?? newFieldId(), name: '[]', type: 'object', children: [] }
      schema = updateNode(schema, parentId, (f) => ({ ...f, items }))
    }
    target = nodeOf({ ...spec, docSchema: schema }, parentId)!.items!.id
  } else if (parent.type !== 'object') throw new OpError('Fields go inside an object or an array.')
  const type: FieldType = kind === 'field' ? 'string' : kind
  const res = addField(schema, target, name ?? (kind === 'field' ? 'field' : kind === 'object' ? 'object' : 'items'), type, index)
  let next = res.schema
  if (kind === 'array') next = updateNode(next, res.id, (f) => ({ ...f, items: { id: newFieldId(), name: '[]', type: 'object', children: [] } }))
  return { spec: withSchema(spec, next), id: res.id }
}

export function renameNode(spec: MappingSpec, id: string, name: string): MappingSpec {
  return withSchema(spec, rename(spec.docSchema, id, name))
}

/** Changes a node's type; a bound node keeps its binding only while its kind still fits. */
export function setNodeType(spec: MappingSpec, id: string, type: FieldType): MappingSpec {
  const n = nodeOf(spec, id)
  if (!n || n.type === type) return spec
  let next = withSchema(spec, setType(spec.docSchema, id, type))
  if (type === 'array') {
    const f = nodeOf(next, id)!
    if (n.type === 'object') next = withSchema(next, updateNode(next.docSchema, id, (x) => ({ ...x, items: { id: f.items!.id, name: '[]', type: 'object', children: n.children ?? [] } })))
  }
  if (type !== 'object' && type !== 'array') next = unbind(next, id)
  return next
}

/** Moves a node up (-1) or down (+1) among its siblings. */
export function moveNode(spec: MappingSpec, id: string, delta: -1 | 1): MappingSpec {
  const ref = findField(spec.docSchema, id)
  if (!ref || ref.index < 0) return spec
  const to = ref.index + (delta > 0 ? 2 : -1)
  if (to < 0 || to > (ref.parent.children?.length ?? 0)) return spec
  return withSchema(spec, move(spec.docSchema, id, ref.parent.id, to))
}

function ids(f: DocField, out = new Set<string>()): Set<string> {
  out.add(f.id)
  for (const c of f.children ?? []) ids(c, out)
  if (f.items) ids(f.items, out)
  return out
}

/** Removes a node with everything under it: their mappings, the relations bound inside, and aggregates. */
export function deleteNode(spec: MappingSpec, id: string): MappingSpec {
  const n = nodeOf(spec, id)
  if (!n) return spec
  const gone = ids(n)
  const rels = new Set<string>()
  for (const r of spec.relations) if (r.targetFieldId && gone.has(r.targetFieldId)) for (const x of withDescendants(spec, r.id)) rels.add(x)
  return {
    ...withSchema(spec, remove(spec.docSchema, id)),
    relations: spec.relations.filter((r) => !rels.has(r.id)),
    aggregates: (spec.aggregates ?? []).filter((a) => !gone.has(a.targetFieldId) && !rels.has(a.relationId)),
    fieldMappings: spec.fieldMappings.filter((m) => !gone.has(m.targetFieldId) && !m.sources.some((s) => s.relation && rels.has(s.relation))),
  }
}

/** What importing a design did (OC-059): fields added, and existing names kept as they were. */
export type ImportOutcome = { spec: MappingSpec; added: number; kept: string[] }

/** Named fields (an array's item node isn't one), as in the server's import report. */
const countFields = (fs: DocField[] = []): number => fs.reduce((n, f) => n + 1 + countFields(f.children) + countFields(f.items?.children), 0)

/**
 * Applies a design the server inferred from the customer's JSON or JSON Schema (OC-059).
 * Onto the root (nodeId null) it replaces the design, with its reference, examples and constant
 * hints, and clears bindings and mappings. Onto an object (or an array's item object) it adds
 * the fields whose names aren't there yet: existing fields keep their ids, bindings and
 * mappings. Onto an array whose items aren't an object, the imported object becomes its item.
 */
export function importDesign(spec: MappingSpec, incoming: DocSchema, nodeId: string | null): ImportOutcome {
  const fields = incoming.root.children ?? []
  if (nodeId === null) {
    // The customer's design is the document: a type field it doesn't have is turned off.
    const tf = spec.typeField
    const typeField = tf.enabled && !fields.some((f) => f.name === tf.name) ? { ...tf, enabled: false } : tf
    const next: MappingSpec = {
      ...spec,
      typeField,
      relations: [],
      aggregates: [],
      fieldMappings: [],
      docSchema: { ...incoming, root: { ...incoming.root, id: spec.docSchema.root.id } },
    }
    return { spec: next, added: countFields(fields), kept: [] }
  }
  const n = nodeOf(spec, nodeId)
  if (!n) throw new OpError('Unknown node.')
  const hintsFor = (added: DocField[]) => {
    const ids = new Set<string>()
    const walk = (f: DocField) => {
      ids.add(f.id)
      f.children?.forEach(walk)
      if (f.items) walk(f.items)
    }
    added.forEach(walk)
    return (incoming.constantHints ?? []).filter((h) => ids.has(h.fieldId))
  }
  const withHints = (next: MappingSpec, added: DocField[]): MappingSpec => {
    const hints = [...(spec.docSchema.constantHints ?? []), ...hintsFor(added)]
    return { ...next, docSchema: { ...next.docSchema, ...(hints.length ? { constantHints: hints } : {}) } }
  }
  if (n.type === 'array' && n.items?.type !== 'object') {
    const item: DocField = { id: newFieldId(), name: '[]', type: 'object', children: fields }
    return { spec: withHints(withSchema(spec, updateNode(spec.docSchema, nodeId, (f) => ({ ...f, items: item }))), fields), added: countFields(fields), kept: [] }
  }
  const container = containerOf(spec, nodeId)
  if (!container) throw new OpError('Import onto an object or an array.')
  const kids = [...(container.children ?? [])]
  const added: DocField[] = []
  const kept: string[] = []
  for (const f of fields) {
    if (kids.some((c) => c.name === f.name)) kept.push(f.name)
    else {
      kids.push(f)
      added.push(f)
    }
  }
  const next = withSchema(spec, updateNode(spec.docSchema, container.id, (f) => ({ ...f, children: kids })))
  return { spec: withHints(next, added), added: countFields(added), kept }
}

/**
 * Adopts a reconciled design (OC-061): fields that still exist kept their ids, so their
 * mappings and bindings stay; what pointed at removed fields goes (mappings, relations bound
 * to them with the relations nested under those, aggregates).
 */
export function adoptDesign(spec: MappingSpec, docSchema: DocSchema): MappingSpec {
  const exists = new Set<string>([docSchema.root.id])
  const walk = (f: DocField) => {
    exists.add(f.id)
    f.children?.forEach(walk)
    if (f.items) walk(f.items)
  }
  docSchema.root.children?.forEach(walk)
  const rels = new Set<string>()
  for (const r of spec.relations) if (r.targetFieldId && !exists.has(r.targetFieldId)) for (const x of withDescendants(spec, r.id)) rels.add(x)
  return {
    ...spec,
    docSchema,
    relations: spec.relations.filter((r) => !rels.has(r.id)),
    aggregates: (spec.aggregates ?? []).filter((a) => exists.has(a.targetFieldId) && !rels.has(a.relationId)),
    fieldMappings: spec.fieldMappings.filter((m) => exists.has(m.targetFieldId) && !m.sources.some((s) => s.relation && rels.has(s.relation))),
  }
}

/** Keeps a constant hint's value as the field's constant (OC-059); the hint is removed. */
export function applyConstantHint(spec: MappingSpec, fieldId: string): MappingSpec {
  const hint = spec.docSchema.constantHints?.find((h) => h.fieldId === fieldId)
  if (!hint) return spec
  return dismissConstantHint(setConstant(spec, fieldId, hint.value), fieldId)
}

/** Drops a constant hint without applying it. */
export function dismissConstantHint(spec: MappingSpec, fieldId: string): MappingSpec {
  const hints = (spec.docSchema.constantHints ?? []).filter((h) => h.fieldId !== fieldId)
  const docSchema = { ...spec.docSchema }
  if (hints.length) docSchema.constantHints = hints
  else delete docSchema.constantHints
  return { ...spec, docSchema }
}

/**
 * Re-points mappings that read the parent row where each row is meant (OC-065): fields in the
 * scope of relationId reading its parent scope's columns read the relation's same-named column.
 * Only for a relation on the parent's own table (the group's rows): elsewhere a parent value is
 * a legitimate choice.
 */
export function rebaseSources(spec: MappingSpec, relationId: string): MappingSpec {
  const r = spec.relations.find((x) => x.id === relationId)
  if (!r) return spec
  const parent = r.parentId ?? ''
  const parentTable = parent ? spec.relations.find((x) => x.id === parent)?.table : spec.root?.table
  if (!r.group && !(parentTable && parentTable.schema === r.table.schema && parentTable.name === r.table.name)) return spec
  const scopes = fieldScopes(spec)
  return {
    ...spec,
    fieldMappings: spec.fieldMappings.map((m) =>
      scopes.get(m.targetFieldId) === relationId && m.sources.some((src) => (src.relation ?? '') === parent && !src.from)
        ? { ...m, sources: m.sources.map((src) => ((src.relation ?? '') === parent && !src.from ? { ...src, relation: relationId } : src)) }
        : m,
    ),
  }
}

/** The "Read it from each row" fix (OC-065): one mapping's parent-row sources read relationId. */
export function repointSource(spec: MappingSpec, mappingId: string, relationId: string): MappingSpec {
  const r = spec.relations.find((x) => x.id === relationId)
  if (!r) return spec
  const parent = r.parentId ?? ''
  return {
    ...spec,
    fieldMappings: spec.fieldMappings.map((m) =>
      m.id === mappingId ? { ...m, sources: m.sources.map((src) => ((src.relation ?? '') === parent && !src.from ? { ...src, relation: relationId } : src)) } : m,
    ),
  }
}

/** The default name of the array holding a table's rows: order_detail → orderDetails. */
export function pluralName(table: string): string {
  const base = renameStyle(table)
  if (/[^s]s$/.test(base)) return base // already plural: order_items → orderItems
  if (/y$/.test(base) && !/[aeiou]y$/.test(base)) return base.replace(/y$/, 'ies')
  if (/(s|x|z|ch|sh)$/.test(base)) return `${base}es`
  return `${base}s`
}

/**
 * Combines the rows that share a document key into one document (OC-066): groups the driving
 * table by the key's columns, fills an array (a new one, or an existing root array) with the
 * group's rows, and moves the chosen root fields mapped from other columns into the array's
 * item, re-pointed to each row. The rest stay at the root (the group's first row): the key's
 * columns, and values every row shares such as type.
 */
/** Root fields that combining would move into the rows: mapped from columns other than the key's. */
export function combinable(spec: MappingSpec, groupBy: string[]): DocField[] {
  const grouped = new Set(groupBy)
  return (spec.docSchema.root.children ?? []).filter((f) => {
    const m = spec.fieldMappings.find((x) => x.targetFieldId === f.id && x.state === 'active')
    return f.type !== 'object' && f.type !== 'array' && !!m && m.sources.length > 0 && m.sources.every((src) => !src.relation && !src.from) && m.sources.some((src) => !grouped.has(src.column))
  })
}

/** Names that usually hold the same value on every row of a group (they stay at the root by default). */
export const sharedNames = /^(type|doc_?type|_type|kind|schema_?version|version)$/i

export function combineRows(spec: MappingSpec, opts: { groupBy: string[]; driving: TableRef; arrayName: string; arrayId?: string; orderBy: { column: string }[]; move?: string[] }): { spec: MappingSpec; arrayId: string } {
  let s = setGroupBy(spec, opts.groupBy, opts.driving)
  let arrayId = opts.arrayId
  if (!arrayId) {
    const added = addNode(s, s.docSchema.root.id, 'array', opts.arrayName)
    s = added.spec
    arrayId = added.id
  }
  const g = bindGroupRows(s, arrayId, opts.orderBy)
  s = g.spec
  const itemId = nodeOf(s, arrayId)!.items!.id
  // What moves into each entry: the given fields, else every combinable one but shared names.
  const moving = combinable(s, opts.groupBy).filter((f) => (opts.move ? opts.move.includes(f.id) : !sharedNames.test(f.name)))
  let schema = s.docSchema
  for (const f of moving) schema = move(schema, f.id, itemId, (nodeOf({ ...s, docSchema: schema }, itemId)!.children ?? []).length)
  const moved = new Set(moving.map((f) => f.id))
  s = {
    ...withSchema(s, schema),
    fieldMappings: s.fieldMappings.map((m) => (moved.has(m.targetFieldId) ? { ...m, sources: m.sources.map((src) => (src.relation ? src : { ...src, relation: g.relationId })) } : m)),
  }
  return { spec: s, arrayId }
}

/** A foreign key from a field's column to the table it references (OC-064). */
export type ForeignKey = { table: TableRef; join: { parent: string; child: string }[]; virtual?: boolean }

/** A simple singular of a table name for the embedded object: products → product, categories → category. */
export function singularName(table: string): string {
  const base = renameStyle(table)
  if (/ies$/.test(base)) return base.replace(/ies$/, 'y')
  if (/(s|x|z|ch|sh)es$/.test(base)) return base.replace(/es$/, '')
  return base.replace(/s$/, '')
}

/**
 * Embeds the row a foreign-key field refers to (OC-064): an object bound many-to-one through the
 * key, with fields generated from columns. "replace" puts it where the id field was (the id and
 * its mapping go); "alongside" adds it right after the id.
 */
export function expandForeignKey(spec: MappingSpec, fieldId: string, fk: ForeignKey, opts: { mode: 'replace' | 'alongside'; name: string; columns: SourceColumn[] }): { spec: MappingSpec; id: string } {
  const ref = findField(spec.docSchema, fieldId)
  if (!ref) throw new OpError('Unknown field.')
  if (ref.field.type === 'object' || ref.field.type === 'array') throw new OpError('Embed from a field that holds the key.')
  const holder = ref.parent.id // an object, the root, or an array's item object
  let next = spec
  let index = ref.index + 1
  if (opts.mode === 'replace') {
    next = deleteNode(spec, fieldId)
    index = ref.index
  }
  const added = addNode(next, holder, 'object', opts.name, index)
  const bound = bind(added.spec, added.id, { kind: 'many-to-one', table: fk.table, join: fk.join, generate: opts.columns })
  return { spec: bound.spec, id: added.id }
}

/**
 * The reverse of expandForeignKey (OC-064): an object bound many-to-one on one column becomes a
 * field mapped from that column of its scope (productId ← product_id); the relation, what
 * nests under it and the object's fields go.
 */
export function collapseToId(spec: MappingSpec, nodeId: string, opts: { name?: string; type?: FieldType } = {}): MappingSpec {
  const r = boundRelations(spec).get(nodeId)
  const n = nodeOf(spec, nodeId)
  if (!r || !n || n.type !== 'object' || r.kind !== 'many-to-one' || r.join?.length !== 1) throw new OpError('Collapse an object bound many-to-one on one column.')
  const column = r.join[0]!.parent
  const scope = r.parentId ?? ''
  const gone = ids(n)
  gone.delete(nodeId)
  let next = unbind(spec, nodeId)
  next = {
    ...next,
    fieldMappings: next.fieldMappings.filter((m) => !gone.has(m.targetFieldId) && m.targetFieldId !== nodeId),
    aggregates: (next.aggregates ?? []).filter((a) => !gone.has(a.targetFieldId)),
  }
  const name = opts.name ?? `${n.name}Id`
  next = withSchema(next, updateNode(next.docSchema, nodeId, (f) => ({ id: f.id, name, type: opts.type ?? 'integer', ...(f.required ? { required: true } : {}) })))
  const source = scope ? { relation: scope, column } : { column }
  return { ...next, fieldMappings: [...next.fieldMappings, { id: newMappingId(), targetFieldId: nodeId, sources: [source], transforms: [], origin: 'manual', state: 'active' }] }
}

/** A binding suggestion's candidate (OC-060), applied with the Bind dialog's operation. */
export type SuggestedBinding = Pick<Relation, 'kind' | 'table' | 'join' | 'via' | 'shape' | 'valueColumn'> & { fromParent?: boolean; adapter?: Adapter }

/** Accepts a suggested binding: binds the node (a "map from parent" suggestion binds nothing). */
export function acceptSuggestion(spec: MappingSpec, nodeId: string, c: Partial<SuggestedBinding> & { group?: boolean; groupBy?: string[]; orderBy?: { column: string; desc?: boolean }[] }): MappingSpec {
  if (c.group && c.groupBy?.length && c.table) {
    // Group the driving table, then fill the array with the group's rows (OC-063).
    return bindGroupRows(setGroupBy(spec, c.groupBy, c.table), nodeId, c.orderBy ?? []).spec
  }
  if (c.fromParent || !c.table || !c.kind) return spec
  const input: BindInput = { kind: c.kind, table: c.table }
  if (c.join) input.join = c.join
  if (c.via) input.via = c.via
  if (c.shape) input.shape = c.shape
  if (c.valueColumn) input.valueColumn = c.valueColumn
  if (c.adapter) input.adapter = c.adapter
  return bind(spec, nodeId, input).spec
}

/** Objects and arrays below the root that aren't bound (OC-060's "N unbound"). */
export function unboundContainers(spec: MappingSpec): { arrays: number; objects: number } {
  const bound = new Set(spec.relations.filter((r) => !r.lookupOnly && r.targetFieldId).map((r) => r.targetFieldId))
  const out = { arrays: 0, objects: 0 }
  const visit = (f: DocField) => {
    for (const c of f.children ?? []) {
      if (c.type === 'array' && !bound.has(c.id)) out.arrays++
      if (c.type === 'object' && !bound.has(c.id)) out.objects++
      visit(c)
      if (c.items) visit(c.items)
    }
  }
  visit(spec.docSchema.root)
  return out
}

/** An empty design (the "blank" start). */
export function blank(spec: MappingSpec): MappingSpec {
  return { ...spec, relations: [], aggregates: [], fieldMappings: [], docSchema: { root: { ...spec.docSchema.root, children: [] }, source: 'manual' } }
}

/** What a binding adds (the Bind dialog's result). */
export type BindInput = Pick<Relation, 'kind' | 'table' | 'join' | 'via' | 'orderBy' | 'limit' | 'filter' | 'shape' | 'overflow' | 'empty' | 'valueColumn' | 'keyColumn'> & {
  adapter?: Adapter
  /** Adds a field per column of the table (the container's existing names are kept). */
  generate?: SourceColumn[]
}

/** The relation whose rows a node sits in ('' = the root): a bound node's parent relation. */
export function scopeOf(spec: MappingSpec, nodeId: string): string {
  if (nodeId === spec.docSchema.root.id) return ''
  return fieldScopes(spec).get(nodeId) ?? ''
}

/**
 * Binds an object or array node to a relation of its scope (OC-047): a relation whose
 * targetFieldId is the node, nested under the node's scope. Binding a bound node again keeps
 * the relation's id, so what's nested and mapped under it stays valid.
 */
export function bind(spec: MappingSpec, nodeId: string, input: BindInput): { spec: MappingSpec; relationId: string } {
  const n = nodeOf(spec, nodeId)
  if (!n || (n.type !== 'object' && n.type !== 'array')) throw new OpError('Bind an object or an array.')
  const many = input.kind !== 'many-to-one'
  const adapter = input.adapter ?? (n.type === 'object' && many ? 'first' : n.type === 'array' && !many ? 'wrap' : undefined)
  const existing = boundRelations(spec).get(nodeId)
  const id = existing?.id ?? newRelationId()
  const parent = scopeOf(spec, nodeId)
  const { generate } = input
  const rest: Partial<BindInput> = { ...input }
  delete rest.generate
  delete rest.adapter
  const rel = { ...rest, id, parentId: parent || null, targetFieldId: nodeId } as Relation
  if (rel.kind === 'many-to-many') delete rel.join
  else delete rel.via
  let docSchema = updateNode(spec.docSchema, nodeId, (f) => {
    const next: DocField = { ...f, binding: { relationId: id, ...(adapter ? { adapter } : {}) } }
    if (f.type === 'array' && (rel.shape ?? 'objects') === 'objects' && f.items?.type !== 'object') next.items = { id: f.items?.id ?? newFieldId(), name: '[]', type: 'object', children: [] }
    return next
  })
  let out: MappingSpec = { ...spec, docSchema: { ...docSchema, source: 'manual' }, relations: existing ? spec.relations.map((r) => (r.id === id ? rel : r)) : [...spec.relations, rel] }
  if (generate?.length) {
    const container = containerOf(out, nodeId)
    if (container) {
      const kids = [...(container.children ?? [])]
      for (const c of generate) {
        const name = uniqueName(kids, renameStyle(c.name, 'camelCase'))
        const { type, format } = fieldTypeOf(c)
        kids.push({ id: newFieldId(), name, type, ...(format ? { format } : {}) })
      }
      docSchema = updateNode(out.docSchema, container.id, (f) => ({ ...f, children: kids }))
      out = { ...out, docSchema }
    }
  }
  // Rows of the parent's own table: what was mapped from the parent row reads each row (OC-065).
  return { spec: rebaseSources(out, id), relationId: id }
}

/** Unbinds a node: its relation and the relations nested under it go, with the mappings reading them; the fields stay. */
export function unbind(spec: MappingSpec, nodeId: string): MappingSpec {
  const r = boundRelations(spec).get(nodeId)
  if (!r) return spec
  const gone = withDescendants(spec, r.id)
  const docSchema = updateNode(spec.docSchema, nodeId, (f) => {
    const next = { ...f }
    delete next.binding
    return next
  })
  return {
    ...spec,
    docSchema,
    relations: spec.relations.filter((x) => !gone.has(x.id)),
    aggregates: (spec.aggregates ?? []).filter((a) => !gone.has(a.relationId)),
    fieldMappings: spec.fieldMappings.filter((m) => !m.sources.some((s) => s.relation && gone.has(s.relation))),
  }
}

/** Sets a bound node's adapter (first or wrap), or clears it. */
export function setAdapter(spec: MappingSpec, nodeId: string, adapter: Adapter | undefined): MappingSpec {
  const r = boundRelations(spec).get(nodeId)
  if (!r) return spec
  return { ...spec, docSchema: updateNode(spec.docSchema, nodeId, (f) => ({ ...f, binding: { relationId: r.id, ...(adapter ? { adapter } : {}) } })) }
}

/** Adds a lookup-only relation in a node's scope: its columns can fill any field there ("values only"). */
export function addLookup(spec: MappingSpec, nodeId: string, input: Pick<Relation, 'table' | 'join'>): { spec: MappingSpec; relationId: string } {
  const id = newRelationId()
  const container = nodeOf(spec, nodeId)
  // A bound node's own relation is the scope of its fields.
  const scope = container && boundRelations(spec).get(nodeId) ? boundRelations(spec).get(nodeId)!.id : scopeOf(spec, nodeId)
  const rel: Relation = { id, parentId: scope || null, kind: 'many-to-one', lookupOnly: true, table: input.table, join: input.join }
  return { spec: { ...spec, relations: [...spec.relations, rel] }, relationId: id }
}

/** Drives a composed mapping from another table or view (OC-047): bindings stay; broken ones are flagged. */
export function setDrivingTable(spec: MappingSpec, table: TableRef): MappingSpec {
  // Another table has other columns: grouping (and the group's rows) don't carry over.
  const group = spec.relations.find((r) => r.group)
  const base = group?.targetFieldId ? unbind(spec, group.targetFieldId) : spec
  return { ...base, mode: 'composed', root: { table } }
}

/** The group's rows mirror the server's SyncGroup (OC-063): the driving table to itself on the group columns. */
export function syncGroup(spec: MappingSpec): MappingSpec {
  const root = spec.root
  if (!root?.table || !spec.relations.some((r) => r.group)) return spec
  const join = (root.groupBy ?? []).map((c) => ({ parent: c, child: c }))
  return { ...spec, relations: spec.relations.map((r) => (r.group ? { ...r, table: root.table, kind: 'one-to-many', join, parentId: null } : r)) }
}

/**
 * One document per distinct value of these driving-table columns (OC-063), or per row when
 * columns is empty — which also unbinds the group's rows.
 */
export function setGroupBy(spec: MappingSpec, columns: string[], fallback: TableRef): MappingSpec {
  const table = spec.root?.table ?? fallback
  if (!columns.length) {
    const group = spec.relations.find((r) => r.group)
    const base = group?.targetFieldId ? unbind(spec, group.targetFieldId) : spec
    return { ...base, mode: 'composed', root: { table } }
  }
  return syncGroup({ ...spec, mode: 'composed', root: { table, groupBy: columns }, key: { ...spec.key, template: groupKey(spec.key.template, columns) } })
}

/**
 * A grouped document's key may only use the group columns (KEY_NOT_GROUPED): a key using
 * others becomes its prefix followed by them — order_detail::{order_id}::{line_no} grouped by
 * order_id is order_detail::{order_id}.
 */
export function groupKey(template: string, groupBy: string[]): string {
  const used = [...template.matchAll(/\{\s*([^}|\s]+)/g)].map((m) => m[1]!).filter((c) => !c.startsWith('$'))
  if (used.length && used.every((c) => groupBy.includes(c))) return template
  const prefix = template.includes('{') ? template.slice(0, template.indexOf('{')) : template
  return prefix + groupBy.map((c) => `{${c}}`).join('::')
}

/**
 * Fills an array (or, first row, an object) with the rows of each group (OC-063): a relation
 * from the driving table to itself, ordered by orderBy (the rest of the primary key). There's
 * one: binding another node moves it.
 */
export function bindGroupRows(spec: MappingSpec, nodeId: string, orderBy: { column: string; desc?: boolean }[] = []): { spec: MappingSpec; relationId: string } {
  const root = spec.root
  if (!root?.groupBy?.length) throw new OpError('Choose what makes one document first (“One document per”).')
  const n = nodeOf(spec, nodeId)
  if (!n || (n.type !== 'object' && n.type !== 'array') || nodeId === spec.docSchema.root.id) throw new OpError('The group’s rows fill an array or an object.')
  if (scopeOf(spec, nodeId)) throw new OpError('The group’s rows go directly under the root.')
  const other = spec.relations.find((r) => r.group && r.targetFieldId !== nodeId)
  const base = other?.targetFieldId ? unbind(spec, other.targetFieldId) : spec
  const join = root.groupBy.map((c) => ({ parent: c, child: c }))
  const res = bind(base, nodeId, { kind: 'one-to-many', table: root.table, join, ...(orderBy.length ? { orderBy } : {}) })
  const grouped = { ...res.spec, relations: res.spec.relations.map((r) => (r.id === res.relationId ? { ...r, group: true } : r)) }
  return { spec: rebaseSources(grouped, res.relationId), relationId: res.relationId }
}

/** Fills a field with an expression over in-scope columns ($root, $parent, relations by name; OC-047). */
export function setExpression(spec: MappingSpec, fieldId: string, expr: string): MappingSpec {
  const others = spec.fieldMappings.filter((m) => !(m.targetFieldId === fieldId && m.state !== 'rejected'))
  const id = spec.fieldMappings.find((m) => m.targetFieldId === fieldId && m.state === 'active')?.id ?? newMappingId()
  return {
    ...spec,
    fieldMappings: [...others, { id, targetFieldId: fieldId, sources: [], transforms: [{ op: 'expression', args: { expr } }], origin: 'manual', state: 'active' }],
  }
}
