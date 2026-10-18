import { addField, findField, pathsById, remove, setRequired } from '@/features/docschema/docschemaOps'
import { removeEmbed } from './embeds'
import { acceptAll, activeFor, newMappingId } from './mappingOps'
import type { DocField, FieldMapping, MappingIssue, MappingSpec, TransformStep } from './types'
import type { Selection } from './store'

/**
 * Applies a validation issue's one-click fix to the spec (OC-022 AC2).
 * `enable-create` changes the target, not the spec, so the caller handles it.
 */
export function applyFix(spec: MappingSpec, issue: MappingIssue): MappingSpec {
  switch (issue.fix?.kind) {
    case 'mark-optional':
      return issue.fieldId ? { ...spec, docSchema: setRequired(spec.docSchema, issue.fieldId, false) } : spec
    case 'accept-suggestions':
      return acceptAll(spec)
    case 'add-transform': {
      const step = issue.fix.step as TransformStep | undefined
      if (!step || !issue.fieldMappingId) return spec
      return {
        ...spec,
        fieldMappings: spec.fieldMappings.map((m) => (m.id === issue.fieldMappingId ? { ...m, transforms: [step, ...m.transforms] } : m)),
      }
    }
    case 'repoint-source': {
      // Read it from each row (OC-065): the parent row's sources read the array's relation.
      const rel = spec.relations.find((r) => r.id === issue.fix?.relationId)
      if (!rel || !issue.fieldMappingId) return spec
      const parent = rel.parentId ?? ''
      return {
        ...spec,
        fieldMappings: spec.fieldMappings.map((m) =>
          m.id === issue.fieldMappingId ? { ...m, sources: m.sources.map((src) => ((src.relation ?? '') === parent && !src.from ? { ...src, relation: rel.id } : src)) } : m,
        ),
      }
    }
    case 'add-to-design':
      return issue.fieldId ? addToDesign(spec, issue.fieldId) : spec
    case 'restore-field':
      return restoreField(spec, issue)
    case 'map-field':
      return issue.fieldId ? mapFrom(spec, issue.fieldId, issue.fix.relationId, issue.fix.columns?.[0]) : spec
    case 'replace-step': {
      // The mapping's step of that op, replaced (a now step's format, OC-179).
      const step = issue.fix.step as TransformStep | undefined
      if (!step || !issue.fieldMappingId) return spec
      return {
        ...spec,
        fieldMappings: spec.fieldMappings.map((m) => (m.id === issue.fieldMappingId ? { ...m, transforms: m.transforms.map((x) => (x.op === step.op ? step : x)) } : m)),
      }
    }
    case 'remove-relation': // an unused lookup (OC-177)
      return issue.fix.relationId ? removeEmbed(spec, issue.fix.relationId) : spec
    default:
      return spec
  }
}

/** A field and everything under it, as paths ("items[].sku", the way the server walks a design). */
function branch(f: DocField, path: string, out: { path: string; field: DocField }[] = []) {
  out.push({ path, field: f })
  for (const c of f.children ?? []) branch(c, `${path}.${c.name}`, out)
  if (f.items) branch(f.items, `${path}[]`, out)
  return out
}

/**
 * Adds a field the tool added, with everything under it, to the imported design (OC-070), so
 * conformance (OC-061) stops flagging it and checks it from now on.
 */
export function addToDesign(spec: MappingSpec, fieldId: string): MappingSpec {
  const imported = spec.docSchema.imported
  const ref = findField(spec.docSchema, fieldId)
  if (!imported || !ref) return spec
  const have = new Set((imported.fields ?? []).map((f) => f.path))
  const added = branch(ref.field, ref.path)
    .filter((b) => !have.has(b.path))
    .map((b) => ({ path: b.path, type: b.field.type, ...(b.field.required ? { required: true } : {}) }))
  if (!added.length) return spec
  return { ...spec, docSchema: { ...spec.docSchema, imported: { ...imported, fields: [...(imported.fields ?? []), ...added] } } }
}

/**
 * Removes a field and everything under it (OC-070): the relations filling it go with their
 * mappings and the fields only they fed (removeEmbed), the rest with their mappings.
 */
export function removeBranch(spec: MappingSpec, fieldId: string): MappingSpec {
  const ref = findField(spec.docSchema, fieldId)
  if (!ref) return spec
  const ids = new Set(branch(ref.field, ref.path).map((b) => b.field.id))
  let next = spec
  for (const r of spec.relations) if (r.targetFieldId && ids.has(r.targetFieldId)) next = removeEmbed(next, r.id)
  if (!findField(next.docSchema, fieldId)) return next
  return {
    ...next,
    docSchema: { ...remove(next.docSchema, fieldId), source: 'manual' },
    fieldMappings: next.fieldMappings.filter((m) => !ids.has(m.targetFieldId)),
    aggregates: (next.aggregates ?? []).filter((a) => !ids.has(a.targetFieldId)),
  }
}

/** What *Go to* selects for an issue: its field mapping, else its field. */
export function issueTarget(issue: MappingIssue): Selection {
  if (issue.fieldMappingId) return { kind: 'mapping', id: issue.fieldMappingId }
  if (issue.fieldId) return { kind: 'field', id: issue.fieldId }
  return null
}

/** What *Jump to mapping* selects for a preview row error's dotted field path. */
export function selectionForPath(spec: MappingSpec, path: string | undefined): Selection {
  if (!path) return null
  for (const [id, p] of pathsById(spec.docSchema)) {
    if (p !== path) continue
    const m = activeFor(spec, id)
    return m ? { kind: 'mapping', id: m.id } : { kind: 'field', id }
  }
  return null
}

export const severityOrder = ['error', 'warning', 'info'] as const

export function groupIssues(issues: MappingIssue[]): Record<(typeof severityOrder)[number], MappingIssue[]> {
  const out = { error: [] as MappingIssue[], warning: [] as MappingIssue[], info: [] as MappingIssue[] }
  for (const i of issues) out[i.severity].push(i)
  return out
}

/** Maps field from column of the relation's rows ("" = the root row), replacing what filled it. */
function mapFrom(spec: MappingSpec, fieldId: string, relation: string | undefined, column: string | undefined): MappingSpec {
  if (!column) return spec
  const source = relation ? { relation, column } : { column }
  return {
    ...spec,
    fieldMappings: [
      ...spec.fieldMappings.filter((m) => m.targetFieldId !== fieldId),
      { id: newMappingId(), targetFieldId: fieldId, sources: [source], transforms: [], origin: 'manual', state: 'active' },
    ],
  }
}

/**
 * Re-creates a field of the imported design that's missing here (OC-071), e.g. productId
 * replaced by an embedded product: under its parent, with the imported type, after the
 * imported fields before it, mapped from the column the server matched (issue.fix).
 */
export function restoreField(spec: MappingSpec, issue: MappingIssue): MappingSpec {
  const path = issue.nodePath || issue.path
  const contract = spec.docSchema.imported?.fields ?? []
  const want = contract.find((f) => f.path === path)
  const dot = path.lastIndexOf('.')
  const parentPath = dot < 0 ? '' : path.slice(0, dot)
  const name = path.slice(dot + 1)
  const ids = new Map([...pathsById(spec.docSchema)].map(([id, p]) => [p, id]))
  const parentId = parentPath ? ids.get(parentPath) : spec.docSchema.root.id
  if (!want || !parentId || ids.has(path)) return spec
  // Its imported position: after the last sibling that comes before it in the imported design.
  const parent = parentPath ? findField(spec.docSchema, parentId)!.field : spec.docSchema.root
  const before = contract.slice(0, contract.indexOf(want)).map((f) => f.path)
  let index = 0
  ;(parent.children ?? []).forEach((c, i) => {
    if (before.includes(parentPath ? `${parentPath}.${c.name}` : c.name)) index = i + 1
  })
  const added = addField(spec.docSchema, parentId, name, want.type, index)
  return mapFrom({ ...spec, docSchema: added.schema }, added.id, issue.fix?.relationId, issue.fix?.columns?.[0])
}

/**
 * What fills a field, in words (the server's DescribeMapping): "from carts.total", "the
 * constant "web"", "a computed value", "not populated". driving is the driving table's name.
 */
export function describeMapping(spec: MappingSpec, driving: string, m: FieldMapping): string {
  if (m.state === 'notPopulated') return 'not populated'
  if (m.computed) return 'a computed value'
  if (m.sources.length) {
    const cols = m.sources.map((src) => {
      const r = src.relation ? spec.relations.find((x) => x.id === src.relation) : undefined
      const table = !src.relation ? driving : !r ? src.relation : src.from === 'via' && r.via ? r.via.table.name : r.table.name
      return table ? `${table}.${src.column}` : src.column
    })
    return `from ${cols.join(' + ')}`
  }
  const step = m.transforms.find((x) => x.op === 'constant' || x.op === 'expression')
  if (step?.op === 'constant') return `the constant ${JSON.stringify(step.args?.value)}`
  if (step?.op === 'expression') return 'an expression'
  return `mapping ${m.id}`
}

/** The mappings that fill a field (a DUPLICATE_TARGET's candidates): active or not populated. */
export const fillersOf = (spec: MappingSpec, fieldId: string) => spec.fieldMappings.filter((m) => m.targetFieldId === fieldId && (m.state === 'active' || m.state === 'notPopulated'))

/** Keeps one of a field's mappings and drops the others that fill it. */
export function keepOnly(spec: MappingSpec, fieldId: string, keep: string): MappingSpec {
  const drop = new Set(fillersOf(spec, fieldId).filter((m) => m.id !== keep).map((m) => m.id))
  return drop.size ? { ...spec, fieldMappings: spec.fieldMappings.filter((m) => !drop.has(m.id)) } : spec
}
