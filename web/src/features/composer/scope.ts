import { fieldScopes, inScope, scopeChain } from '@/features/mapping/embeds'
import type { DocField, MappingSpec, Relation } from '@/features/mapping/types'

export { fieldScopes, inScope, scopeChain }

// The composer's scope rule (OC-047), mirroring backend/internal/mapping/scope.go: a field can
// read the root table, every relation bound on the path from the root to the field, and the
// lookup-only relations attached to any of them. scope.test.ts runs the shared fixture
// backend/internal/mapping/testdata/scope-cases.json.

export type TableRef = { schema: string; name: string }

export type ScopeEntry = {
  /** '' is the root (driving) table. */
  relationId: string
  table: TableRef
  lookup?: boolean
  /** A many-to-many relation's junction: its payload columns are in scope too. */
  via?: TableRef
}

const parentOf = (r: Relation) => r.parentId ?? ''

/** The relation bound to each node (its targetFieldId), lookups aside. */
export function boundRelations(spec: MappingSpec): Map<string, Relation> {
  const out = new Map<string, Relation>()
  for (const r of spec.relations) if (!r.lookupOnly && r.targetFieldId) out.set(r.targetFieldId, r)
  return out
}

/** What a field can be mapped from, outermost first (the inspector lists only these columns). */
export function scope(spec: MappingSpec, fieldId: string): ScopeEntry[] | undefined {
  const scopes = fieldScopes(spec)
  const at = scopes.get(fieldId)
  if (at === undefined) return undefined
  const levels = ['', ...scopeChain(spec, at).reverse()]
  const out: ScopeEntry[] = []
  for (const id of levels) {
    if (id === '') out.push({ relationId: '', table: spec.root?.table ?? { schema: '', name: '' } })
    else {
      const r = spec.relations.find((x) => x.id === id)
      if (!r) continue
      out.push({ relationId: id, table: r.table, ...(r.via && r.via.columns?.length ? { via: r.via.table } : {}) })
    }
    for (const r of spec.relations) if (r.lookupOnly && parentOf(r) === id) out.push({ relationId: r.id, table: r.table, lookup: true })
  }
  return out
}

/**
 * The names a composer expression can use at a field besides its row's columns (autocompletion,
 * mirroring transform.ExpressionNames): $root, $parent, then every in-scope relation by id, by
 * the name of the field it fills and by its table's name. Names that are also columns of the
 * field's own row are left out (the column wins).
 */
export function expressionNames(spec: MappingSpec, fieldId: string, rowColumns: string[] = []): string[] {
  const entries = scope(spec, fieldId)
  if (!entries) return []
  const reserved = new Set(['value', 'row', 'upper', 'lower', 'trim', 'len', 'round', 'coalesce', 'now', 'concat', ...rowColumns])
  const names = new Set<string>()
  const fields = new Map<string, string>()
  const index = (f: DocField) => {
    fields.set(f.id, f.name)
    for (const c of f.children ?? []) index(c)
    if (f.items) index(f.items)
  }
  index(spec.docSchema.root)
  for (const e of entries) {
    if (e.relationId === '') continue
    const r = spec.relations.find((x) => x.id === e.relationId)!
    const fieldName = !r.lookupOnly && r.targetFieldId ? fields.get(r.targetFieldId) : undefined
    for (const n of [r.id, fieldName, r.table.name]) if (n && !reserved.has(n)) names.add(n)
  }
  return ['$root', '$parent', ...[...names].sort()]
}
