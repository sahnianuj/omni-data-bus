import type { DocField, DocSchema, FieldType, SourceColumn } from '@/features/mapping/types'
import { rename, words, type NamingStyle } from '@/lib/naming'
import { makeField, newFieldId, uniqueName } from './docschemaOps'

// Mirrors docschema.FromTable (backend/internal/docschema/generate.go).

export function fieldTypeOf(c: Pick<SourceColumn, 'jsonType' | 'udtName'>): { type: FieldType; format?: string } {
  switch (c.jsonType) {
    case 'integer':
    case 'number':
    case 'boolean':
    case 'any':
    case 'array':
      return { type: c.jsonType }
    case 'datetime':
      return { type: 'datetime', format: c.udtName === 'date' ? 'date' : 'iso8601' }
  }
  return c.udtName === 'uuid' ? { type: 'string', format: 'uuid' } : { type: 'string' }
}

function fieldFor(c: SourceColumn, name: string): DocField {
  const { type, format } = fieldTypeOf(c)
  const f: DocField = { ...makeField(name, type), ...(format ? { format } : {}) }
  if (!c.nullable) f.required = true
  if (type === 'array') {
    const it = fieldTypeOf({ jsonType: c.elementType ?? 'string', udtName: '' })
    f.items = { id: newFieldId(), name: '[]', type: it.type === 'object' || it.type === 'array' ? 'any' : it.type }
  }
  return f
}

export type GenerateOptions = { naming: NamingStyle; nestByPrefix: boolean }

/** One field per column, in column order (OC-015 way 1). */
export function generateFromTable(columns: SourceColumn[], opts: GenerateOptions): DocSchema {
  const root: DocField = { id: 'fld_root', name: '$', type: 'object', children: [] }
  const groups = new Map<string, number>()
  if (opts.nestByPrefix) {
    for (const c of columns) {
      const w = words(c.name).map((x) => x.toLowerCase())
      if (w.length > 1) groups.set(w[0]!, (groups.get(w[0]!) ?? 0) + 1)
    }
  }
  const placed = new Map<string, DocField>()
  for (const c of columns) {
    const w = words(c.name).map((x) => x.toLowerCase())
    if (opts.nestByPrefix && w.length > 1 && (groups.get(w[0]!) ?? 0) >= 2) {
      let obj = placed.get(w[0]!)
      if (!obj) {
        obj = { id: newFieldId(), name: rename(w[0]!, opts.naming), type: 'object', children: [] }
        placed.set(w[0]!, obj)
        root.children!.push(obj)
      }
      const rest = opts.naming === 'preserve' ? c.name.slice(c.name.indexOf('_') + 1) || c.name : w.slice(1).join('_')
      obj.children!.push(fieldFor(c, uniqueName(obj.children!, rename(rest, opts.naming))))
      continue
    }
    root.children!.push(fieldFor(c, uniqueName(root.children!, rename(c.name, opts.naming))))
  }
  return { root, source: 'generated' }
}

/** A sample document from the design with placeholder values (the designer's JSON view). */
export function sampleDocument(schema: DocSchema, typeField?: { enabled: boolean; name: string; value: string }): unknown {
  const value = (f: DocField): unknown => {
    switch (f.type) {
      case 'object':
        return Object.fromEntries((f.children ?? []).map((c) => [c.name, value(c)]))
      case 'array':
        return f.items ? [value(f.items)] : []
      case 'integer':
        return 0
      case 'number':
        return 0.0
      case 'boolean':
        return false
      case 'datetime':
        return f.format === 'date' ? '2026-01-01' : f.format === 'epoch_ms' ? 1767225600000 : '2026-01-01T00:00:00Z'
      case 'any':
        return null
    }
    return f.format === 'uuid' ? '00000000-0000-0000-0000-000000000000' : f.format === 'email' ? 'name@example.com' : 'string'
  }
  const doc = value(schema.root) as Record<string, unknown>
  return typeField?.enabled && typeField.name ? { [typeField.name]: typeField.value, ...doc } : doc
}
