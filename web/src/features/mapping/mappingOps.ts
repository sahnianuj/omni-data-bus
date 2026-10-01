import { addField, OpError, setRequired } from '@/features/docschema/docschemaOps'
import { rename, type NamingStyle } from '@/lib/naming'
import { canConnect } from './canConnect'
import { fieldScopes, inScope } from './embeds'
import type { CanvasColumn, FieldType, MappingSpec, SourceColumn, SourceRef, TransformStep } from './types'

// Pure reducers for mapping edits (OC-018, OC-019). Each returns a new spec; the canvas and
// the table view call the same functions, so both produce identical specs (OC-019 AC7).

export { OpError }

const MAP_ALPHABET = '0123456789abcdefghijklmnopqrstuvwxyz'

export function newMappingId(): string {
  const bytes = new Uint8Array(10)
  crypto.getRandomValues(bytes)
  return 'map_' + Array.from(bytes, (b) => MAP_ALPHABET[b % MAP_ALPHABET.length]).join('')
}

function fieldType(spec: MappingSpec, fieldId: string): FieldType | undefined {
  const visit = (f: MappingSpec['docSchema']['root']): FieldType | undefined => {
    if (f.id === fieldId) return f.type
    for (const c of f.children ?? []) {
      const t = visit(c)
      if (t) return t
    }
    return f.items ? visit(f.items) : undefined
  }
  return visit(spec.docSchema.root)
}

/** The active mapping of a target field, if any. */
export function activeFor(spec: MappingSpec, fieldId: string) {
  return spec.fieldMappings.find((m) => m.targetFieldId === fieldId && m.state === 'active')
}

export type ConnectResult =
  | { spec: MappingSpec; outcome: 'connected'; class: 'exact' | 'lossless' | 'lossy'; mappingId: string }
  | { outcome: 'occupied'; mappingId: string }

/**
 * Maps a column onto a field as a manual mapping (OC-019 AC2), attaching the default
 * conversion for lossless/lossy pairs. An occupied field returns 'occupied' so the UI can ask
 * Replace or Add; incompatible types throw.
 */
export function connect(spec: MappingSpec, column: CanvasColumn, fieldId: string): ConnectResult {
  const target = fieldType(spec, fieldId)
  if (!target) throw new OpError('Unknown target field.')
  checkScope(spec, column, fieldId)
  const cc = canConnect(column.jsonType, target)
  if (!cc.allowed) {
    throw new OpError(`${column.jsonType} → ${target} is not supported; map a JSON column or add fields.`)
  }
  const existing = activeFor(spec, fieldId)
  if (existing) return { outcome: 'occupied', mappingId: existing.id }
  const id = newMappingId()
  const fieldMappings = spec.fieldMappings.filter(
    // A pending suggestion for this field, or an old rejection of this exact pair, is superseded.
    (m) => !(m.targetFieldId === fieldId && m.state === 'pending') && !(m.targetFieldId === fieldId && m.state === 'rejected' && m.sources[0]?.column === sourceOf(column).column),
  )
  fieldMappings.push({
    id, targetFieldId: fieldId, sources: [sourceOf(column)],
    transforms: cc.suggestedTransform ? [{ ...cc.suggestedTransform }] : [],
    origin: 'manual', state: 'active', needsReview: cc.class === 'lossy' || undefined,
  })
  return { spec: { ...spec, fieldMappings }, outcome: 'connected', class: cc.class as 'exact' | 'lossless' | 'lossy', mappingId: id }
}

/** The mapping source for a canvas column (a relation's names its relation; a junction column is from 'via'). */
export function sourceOf(column: CanvasColumn): SourceRef {
  if (!column.embed) return { column: column.name }
  return column.from === 'via' ? { column: column.column ?? column.name, relation: column.embed, from: 'via' } : { column: column.column ?? column.name, relation: column.embed }
}

/**
 * A field can be filled from the root table, the relations on its path and their lookup-only
 * relations (OC-043 scope rule): e.g. items[].orderId from orders.order_id.
 */
function checkScope(spec: MappingSpec, column: CanvasColumn, fieldId: string) {
  if (inScope(spec, fieldScopes(spec).get(fieldId) ?? '', column.embed)) return
  throw new OpError('Columns of an embedded table can only fill fields inside that embed (or the fields of relations nested in it).')
}

const defaultOps = new Set(['toString', 'parseInteger', 'parseNumber', 'parseBoolean', 'toDatetime', 'formatDatetime'])

/** Swaps the source of a mapping (OC-019 AC3 Replace), keeping transforms that still apply. */
export function replaceSource(spec: MappingSpec, mappingId: string, column: CanvasColumn): MappingSpec {
  return {
    ...spec,
    fieldMappings: spec.fieldMappings.map((m) => {
      if (m.id !== mappingId) return m
      const target = fieldType(spec, m.targetFieldId) ?? 'any'
      checkScope(spec, column, m.targetFieldId)
      const cc = canConnect(column.jsonType, target)
      if (!cc.allowed) throw new OpError(`${column.jsonType} → ${target} is not supported.`)
      // A leading default conversion belonged to the old source type: replace it by the new one's.
      let transforms = m.transforms
      if (transforms[0] && defaultOps.has(transforms[0].op)) transforms = transforms.slice(1)
      if (cc.suggestedTransform) transforms = [{ ...cc.suggestedTransform }, ...transforms]
      return { ...m, sources: [sourceOf(column)], transforms, origin: 'manual' as const, state: 'active' as const, needsReview: cc.class === 'lossy' || undefined }
    }),
  }
}

/** Adds a second (third, …) source, combined with concat (OC-019 AC3 Add). */
export function addSource(spec: MappingSpec, mappingId: string, column: string | CanvasColumn): MappingSpec {
  const src: SourceRef = typeof column === 'string' ? { column } : sourceOf(column)
  return {
    ...spec,
    fieldMappings: spec.fieldMappings.map((m) => {
      if (m.id !== mappingId || m.sources.some((s) => s.column === src.column && s.relation === src.relation && s.from === src.from)) return m
      if (!inScope(spec, fieldScopes(spec).get(m.targetFieldId) ?? '', src.relation)) throw new OpError('That column isn\'t in this field\'s scope.')
      const hasConcat = m.transforms.some((t) => t.op === 'concat' || t.op === 'expression')
      const transforms = hasConcat ? m.transforms : [{ op: 'concat', args: { separator: ' ' } }, ...m.transforms.filter((t) => !defaultOps.has(t.op))]
      return { ...m, sources: [...m.sources, src], transforms, origin: 'manual' as const }
    }),
  }
}

/** Removes a manual mapping; auto and suggested ones become rejected (OC-019 AC5). */
export function disconnect(spec: MappingSpec, mappingId: string): MappingSpec {
  const m = spec.fieldMappings.find((x) => x.id === mappingId)
  if (!m) return spec
  if (m.origin === 'manual') return { ...spec, fieldMappings: spec.fieldMappings.filter((x) => x.id !== mappingId) }
  return rejectSuggestion(spec, mappingId)
}

export function acceptSuggestion(spec: MappingSpec, mappingId: string): MappingSpec {
  return {
    ...spec,
    fieldMappings: spec.fieldMappings.map((m) =>
      m.id === mappingId && m.state === 'pending' ? { ...m, state: 'active' as const, origin: 'manual' as const, reason: 'accepted-suggestion' } : m,
    ),
  }
}

export function rejectSuggestion(spec: MappingSpec, mappingId: string): MappingSpec {
  return { ...spec, fieldMappings: spec.fieldMappings.map((m) => (m.id === mappingId ? { ...m, state: 'rejected' as const } : m)) }
}

/** Accepts every pending suggestion at or above minConfidence (0 = all). */
export function acceptAll(spec: MappingSpec, minConfidence = 0): MappingSpec {
  let next = spec
  for (const m of spec.fieldMappings) if (m.state === 'pending' && (m.confidence ?? 0) >= minConfidence) next = acceptSuggestion(next, m.id)
  return next
}

/** Excludes a column from the mapping; its mappings and suggestions go with it (OC-018 AC6). */
export function ignoreColumn(spec: MappingSpec, column: string): MappingSpec {
  if (spec.ignoredColumns.includes(column)) return spec
  return {
    ...spec,
    ignoredColumns: [...spec.ignoredColumns, column],
    fieldMappings: spec.fieldMappings.filter((m) => m.state === 'rejected' || !m.sources.some((s) => s.column === column)),
  }
}

export function unignoreColumn(spec: MappingSpec, column: string): MappingSpec {
  return { ...spec, ignoredColumns: spec.ignoredColumns.filter((c) => c !== column) }
}

const fieldTypeOf = (c: SourceColumn): FieldType => {
  switch (c.jsonType) {
    case 'integer':
    case 'number':
    case 'boolean':
    case 'datetime':
    case 'array':
    case 'any':
      return c.jsonType
  }
  return 'string'
}

/** Creates a field named after the column (naming style respected) and maps it (OC-018 AC6, OC-019 AC4). */
export function createFieldAndMap(spec: MappingSpec, column: CanvasColumn, parentId: string, naming: NamingStyle = 'camelCase'): { spec: MappingSpec; fieldId: string } {
  const added = addField(spec.docSchema, parentId, rename(sourceOf(column).column, naming), fieldTypeOf(column))
  let docSchema = added.schema
  if (!column.nullable) docSchema = setRequired(docSchema, added.id, true)
  const withField = { ...spec, docSchema }
  const res = connect(withField, column, added.id)
  if (res.outcome !== 'connected') throw new OpError('The new field is already mapped.')
  return { spec: res.spec, fieldId: added.id }
}

export function setTransforms(spec: MappingSpec, mappingId: string, transforms: TransformStep[]): MappingSpec {
  return { ...spec, fieldMappings: spec.fieldMappings.map((m) => (m.id === mappingId ? { ...m, transforms } : m)) }
}

/** Fills a field with a constant (a mapping without sources). */
export function setConstant(spec: MappingSpec, fieldId: string, value: unknown): MappingSpec {
  const others = spec.fieldMappings.filter((m) => !(m.targetFieldId === fieldId && m.state !== 'rejected'))
  return {
    ...spec,
    fieldMappings: [...others, { id: newMappingId(), targetFieldId: fieldId, sources: [], transforms: [{ op: 'constant', args: { value } }], origin: 'manual', state: 'active' }],
  }
}

/** Maps nothing onto a field any more (manual removed, auto/suggested rejected). */
export function unmap(spec: MappingSpec, fieldId: string): MappingSpec {
  let next = spec
  for (const m of spec.fieldMappings) if (m.targetFieldId === fieldId && m.state !== 'rejected') next = disconnect(next, m.id)
  return next
}
