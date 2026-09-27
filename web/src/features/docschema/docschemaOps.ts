import type { DocField, DocSchema, FieldType } from '@/features/mapping/types'

// Pure, immutable edits of a document design (OC-015). Unchanged subtrees keep their object
// identity, so rows that didn't change don't re-render. Mirrors the rules of
// backend/internal/docschema.Validate.

export const MAX_DEPTH = 16
export const MAX_FIELDS = 2000

const ALPHABET = '0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ'

export function newFieldId(): string {
  const bytes = new Uint8Array(10)
  crypto.getRandomValues(bytes)
  return 'fld_' + Array.from(bytes, (b) => ALPHABET[b % ALPHABET.length]).join('')
}

export class OpError extends Error {}

export type FieldRef = { field: DocField; path: string; parent: DocField; index: number; depth: number }

/** Every field below the root, depth-first in design order, with its path and parent. */
export function walk(schema: DocSchema): FieldRef[] {
  const out: FieldRef[] = []
  const visit = (parent: DocField, prefix: string, depth: number) => {
    ;(parent.children ?? []).forEach((f, index) => {
      const path = prefix ? `${prefix}.${f.name}` : f.name
      out.push({ field: f, path, parent, index, depth })
      nested(f, path, depth)
    })
  }
  const nested = (f: DocField, path: string, depth: number) => {
    if (f.type === 'object') visit(f, path, depth + 1)
    if (f.type === 'array' && f.items) {
      out.push({ field: f.items, path: `${path}[]`, parent: f, index: -1, depth: depth + 1 })
      nested(f.items, `${path}[]`, depth + 1)
    }
  }
  visit(schema.root, '', 1)
  return out
}

export function pathsById(schema: DocSchema): Map<string, string> {
  return new Map(walk(schema).map((r) => [r.field.id, r.path]))
}

export function findField(schema: DocSchema, id: string): FieldRef | undefined {
  return walk(schema).find((r) => r.field.id === id)
}

export type Leaf = { fieldId: string; path: string; name: string; type: FieldType; itemType?: FieldType; parentNames: string[]; required: boolean; empty: boolean }

/** Mappable fields (mirrors docschema.Schema.Leaves on the server). */
export function leaves(schema: DocSchema): Leaf[] {
  const out: Leaf[] = []
  const visit = (parent: DocField, prefix: string, parents: string[]) => {
    for (const f of parent.children ?? []) {
      const path = prefix ? `${prefix}.${f.name}` : f.name
      const base = { fieldId: f.id, path, name: f.name, parentNames: parents, required: !!f.required }
      if (f.type === 'object' && (f.children?.length ?? 0) > 0) visit(f, path, [...parents, f.name])
      else if (f.type === 'object') out.push({ ...base, type: 'object', empty: true })
      else if (f.type === 'array') {
        if (!f.items) out.push({ ...base, type: 'array', empty: true })
        else if (f.items.type !== 'object' && f.items.type !== 'array') out.push({ ...base, type: 'array', itemType: f.items.type, empty: false })
      } else out.push({ ...base, type: f.type, empty: false })
    }
  }
  visit(schema.root, '', [])
  return out
}

/** Rebuilds the tree, replacing the field with id by fn's result (or removing it on null). */
function mapField(field: DocField, id: string, fn: (f: DocField) => DocField | null): DocField {
  let changed = false
  let children = field.children
  if (children) {
    const next: DocField[] = []
    for (const c of children) {
      if (c.id === id) {
        const r = fn(c)
        if (r !== c) changed = true
        if (r) next.push(r)
      } else {
        const r = mapField(c, id, fn)
        if (r !== c) changed = true
        next.push(r)
      }
    }
    if (changed) children = next
  }
  let items = field.items
  if (items) {
    if (items.id === id) {
      const r = fn(items)
      if (r !== items) changed = true
      items = r ?? undefined
    } else {
      const r = mapField(items, id, fn)
      if (r !== items) {
        items = r
        changed = true
      }
    }
  }
  return changed ? { ...field, children, items } : field
}

function update(schema: DocSchema, id: string, fn: (f: DocField) => DocField | null): DocSchema {
  if (id === schema.root.id) {
    const r = fn(schema.root)
    return r && r !== schema.root ? { ...schema, root: r, source: 'manual' } : schema
  }
  const root = mapField(schema.root, id, fn)
  return root === schema.root ? schema : { ...schema, root, source: 'manual' }
}

function checkName(name: string) {
  if (name.length < 1 || name.length > 128) throw new OpError('Names must be 1–128 characters.')
  if (/[.[]/.test(name)) throw new OpError("Names can't contain '.' or '['.")
}

function siblingsOf(schema: DocSchema, id: string): DocField[] {
  if (id === schema.root.id) return []
  const ref = findField(schema, id)
  return ref && ref.index >= 0 ? (ref.parent.children ?? []) : []
}

export function uniqueName(siblings: DocField[], name: string): string {
  const taken = new Set(siblings.map((s) => s.name))
  if (!taken.has(name)) return name
  for (let i = 2; ; i++) if (!taken.has(`${name}${i}`)) return `${name}${i}`
}

export function makeField(name: string, type: FieldType): DocField {
  const f: DocField = { id: newFieldId(), name, type }
  if (type === 'object') f.children = []
  if (type === 'array') f.items = { id: newFieldId(), name: '[]', type: 'string' }
  return f
}

/** Adds a field to an object (the root by default); the name is made unique. */
export function addField(schema: DocSchema, parentId: string, name: string, type: FieldType, index?: number): { schema: DocSchema; id: string } {
  checkName(name)
  const count = walk(schema).length
  if (count + 1 > MAX_FIELDS) throw new OpError(`A design can have at most ${MAX_FIELDS} fields.`)
  let created: DocField | undefined
  const next = update(schema, parentId, (p) => {
    if (p.type !== 'object') throw new OpError('Fields can only be added to objects.')
    const kids = [...(p.children ?? [])]
    created = makeField(uniqueName(kids, name), type)
    kids.splice(index ?? kids.length, 0, created)
    return { ...p, children: kids }
  })
  return { schema: next, id: created!.id }
}

export function rename(schema: DocSchema, id: string, name: string): DocSchema {
  checkName(name)
  if (siblingsOf(schema, id).some((s) => s.id !== id && s.name === name)) {
    throw new OpError(`Another field at this level is already called '${name}'.`)
  }
  return update(schema, id, (f) => (f.name === name ? f : { ...f, name }))
}

export function setType(schema: DocSchema, id: string, type: FieldType): DocSchema {
  return update(schema, id, (f) => {
    if (f.type === type) return f
    const next: DocField = { ...f, type, children: undefined, items: undefined, format: undefined }
    if (type === 'object') next.children = f.children ?? []
    if (type === 'array') next.items = f.items ?? { id: newFieldId(), name: '[]', type: 'string' }
    if (type === 'datetime') next.format = 'iso8601'
    return next
  })
}

export function setRequired(schema: DocSchema, id: string, required: boolean): DocSchema {
  return update(schema, id, (f) => ({ ...f, required: required || undefined }))
}

export function setDescription(schema: DocSchema, id: string, description: string): DocSchema {
  return update(schema, id, (f) => ({ ...f, description: description || undefined }))
}

export function remove(schema: DocSchema, id: string): DocSchema {
  if (id === schema.root.id) throw new OpError("The document root can't be deleted.")
  return update(schema, id, () => null)
}

function contains(f: DocField, id: string): boolean {
  if (f.id === id) return true
  return (f.children ?? []).some((c) => contains(c, id)) || (!!f.items && contains(f.items, id))
}

/** Moves a field into an object at index; IDs are kept (paths change, mappings follow). */
export function move(schema: DocSchema, id: string, parentId: string, index: number): DocSchema {
  const ref = findField(schema, id)
  if (!ref || ref.index < 0) throw new OpError('Only object fields can be moved.')
  if (contains(ref.field, parentId)) throw new OpError("A field can't be moved into itself.")
  const target = parentId === schema.root.id ? schema.root : findField(schema, parentId)?.field
  if (!target || target.type !== 'object') throw new OpError('Fields can only be moved into objects.')
  if (target.id !== ref.parent.id && (target.children ?? []).some((c) => c.name === ref.field.name)) {
    throw new OpError(`'${target.name}' already has a field called '${ref.field.name}'.`)
  }
  let adj = index
  if (target.id === ref.parent.id && ref.index < index) adj-- // removing first shifts later positions
  const without = remove(schema, id)
  return update(without, parentId, (p) => {
    const kids = [...(p.children ?? [])]
    kids.splice(Math.max(0, Math.min(adj, kids.length)), 0, ref.field)
    return { ...p, children: kids }
  })
}

/** Groups sibling fields into a new object (placed where the first one was). */
export function wrap(schema: DocSchema, ids: string[], name: string): { schema: DocSchema; id: string } {
  checkName(name)
  const refs = ids.map((id) => findField(schema, id))
  if (refs.some((r) => !r || r.index < 0)) throw new OpError('Only object fields can be wrapped.')
  const parent = refs[0]!.parent
  if (refs.some((r) => r!.parent.id !== parent.id)) throw new OpError('Wrap fields that share a parent.')
  const set = new Set(ids)
  const others = (parent.children ?? []).filter((c) => !set.has(c.id))
  const obj: DocField = {
    id: newFieldId(),
    name: uniqueName(others, name),
    type: 'object',
    children: (parent.children ?? []).filter((c) => set.has(c.id)),
  }
  const first = Math.min(...refs.map((r) => r!.index))
  const next = update(schema, parent.id, (p) => {
    const kids = (p.children ?? []).filter((c) => !set.has(c.id))
    kids.splice(first, 0, obj)
    return { ...p, children: kids }
  })
  return { schema: next, id: obj.id }
}

/** Replaces an object by its children (names are made unique among the new siblings). */
export function unwrap(schema: DocSchema, id: string): DocSchema {
  const ref = findField(schema, id)
  if (!ref || ref.field.type !== 'object' || ref.index < 0) throw new OpError('Only objects can be unwrapped.')
  return update(schema, ref.parent.id, (p) => {
    const kids = (p.children ?? []).filter((c) => c.id !== id)
    const lifted: DocField[] = []
    for (const c of ref.field.children ?? []) lifted.push({ ...c, name: uniqueName([...kids, ...lifted], c.name) })
    kids.splice(ref.index, 0, ...lifted)
    return { ...p, children: kids }
  })
}

/** Copies a field (with new IDs) right after it. */
export function duplicate(schema: DocSchema, id: string): { schema: DocSchema; id: string } {
  const ref = findField(schema, id)
  if (!ref || ref.index < 0) throw new OpError('Only object fields can be duplicated.')
  const clone = (f: DocField): DocField => ({ ...f, id: newFieldId(), children: f.children?.map(clone), items: f.items ? clone(f.items) : undefined })
  const copy = { ...clone(ref.field), name: uniqueName(ref.parent.children ?? [], `${ref.field.name}Copy`) }
  const next = update(schema, ref.parent.id, (p) => {
    const kids = [...(p.children ?? [])]
    kids.splice(ref.index + 1, 0, copy)
    return { ...p, children: kids }
  })
  return { schema: next, id: copy.id }
}

export type Issue = { path: string; fieldId: string; message: string }

/** Client-side validation (the server's docschema.Validate rules). */
export function validate(schema: DocSchema): Issue[] {
  const issues: Issue[] = []
  const refs = walk(schema)
  if (refs.length > MAX_FIELDS) issues.push({ path: '$', fieldId: schema.root.id, message: `A design can have at most ${MAX_FIELDS} fields.` })
  const seen = new Map<string, Set<string>>()
  for (const r of refs) {
    if (r.index < 0) continue
    const names = seen.get(r.parent.id) ?? new Set<string>()
    if (!r.field.name || r.field.name.length > 128) issues.push({ path: r.path, fieldId: r.field.id, message: 'Names must be 1–128 characters.' })
    else if (/[.[]/.test(r.field.name)) issues.push({ path: r.path, fieldId: r.field.id, message: "Names can't contain '.' or '['." })
    else if (names.has(r.field.name)) issues.push({ path: r.path, fieldId: r.field.id, message: `Another field at this level is already called '${r.field.name}'.` })
    names.add(r.field.name)
    seen.set(r.parent.id, names)
    if (r.depth > MAX_DEPTH) issues.push({ path: r.path, fieldId: r.field.id, message: `Designs can nest at most ${MAX_DEPTH} levels.` })
    if (r.field.type === 'array' && !r.field.items) issues.push({ path: r.path, fieldId: r.field.id, message: 'An array needs an item type.' })
  }
  return issues
}
