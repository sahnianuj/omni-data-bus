import { fieldScopes, srcKey } from './embeds'
import type { FieldMapping, MappingSpec } from './types'

// A name, optionally followed by .column: `row.first_name`, `$root.id`, `partners.number`, `total`.
const REF = /(?<![\w$.])(\$?[A-Za-z_][A-Za-z0-9_]*)(?:\s*\.\s*([A-Za-z_][A-Za-z0-9_]*))?/g
const STRINGS = /'(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*"|`(?:[^`\\]|\\.)*`/g

/**
 * The source columns each mapping reads, as srcKeys: its sources, then the columns its
 * expressions name, resolved as the server's expression scope does (transform/composed.go):
 * a bare column of the field's scope, `row.x`, `$root.x`, `$parent.x`, and a relation's
 * `id.x`, `table.x` or `field.x`. known (the canvas's column keys) drops names that aren't
 * columns, such as helpers. A column used only by concat(row.first_name, ' ', row.last_name)
 * then counts as used.
 */
export function mappingReads(spec: MappingSpec, known: ReadonlySet<string>): (m: FieldMapping) => string[] {
  const scopes = fieldScopes(spec)
  const parentOf = new Map(spec.relations.map((r) => [r.id, r.parentId ?? '']))
  const byName = new Map<string, string>()
  const alias = (n: string | undefined, id: string) => {
    if (n && !byName.has(n)) byName.set(n, id)
  }
  for (const r of spec.relations) alias(r.id, r.id)
  for (const r of spec.relations) alias(r.table.name, r.id)
  const names = new Map<string, string>()
  const walk = (f: MappingSpec['docSchema']['root']) => {
    names.set(f.id, f.name)
    for (const c of f.children ?? []) walk(c)
    if (f.items) walk(f.items)
  }
  walk(spec.docSchema.root)
  for (const r of spec.relations) if (!r.lookupOnly && r.targetFieldId) alias(names.get(r.targetFieldId), r.id)

  const key = (column: string, relation: string) => srcKey({ column, relation: relation || undefined })
  return (m) => {
    const out = new Set(m.sources.map(srcKey))
    const scope = scopes.get(m.targetFieldId) ?? ''
    for (const t of m.transforms) {
      if (t.op !== 'expression' || typeof t.args?.expr !== 'string') continue
      for (const [, name, col] of t.args.expr.replace(STRINGS, ' ').matchAll(REF)) {
        let k: string | undefined
        if (col === undefined) k = key(name!, scope) // a bare column of the scope
        else if (name === 'row') k = key(col, scope)
        else if (name === '$root') k = key(col, '')
        else if (name === '$parent') k = key(col, parentOf.get(scope) ?? '')
        else if (byName.has(name!)) k = key(col, byName.get(name!)!)
        if (k && known.has(k)) out.add(k)
      }
    }
    return [...out]
  }
}

/**
 * The lookups ("values only" relations) nothing reads (OC-177), as the server's LOOKUP_UNUSED:
 * no mapping's source, no expression naming it (its id or table, then "."), no total, no
 * relation under it.
 */
export function unusedLookups(spec: MappingSpec): Set<string> {
  const used = new Set<string>()
  const exprs: string[] = []
  for (const m of spec.fieldMappings) {
    if (m.state === 'rejected') continue
    for (const s of m.sources) used.add(s.relation ?? '')
    for (const t of m.transforms) if (t.op === 'expression' && typeof t.args?.expr === 'string') exprs.push(t.args.expr)
  }
  for (const a of spec.aggregates ?? []) used.add(a.relationId)
  for (const r of spec.relations) used.add(r.parentId ?? '')
  const named = (name: string) => {
    const re = new RegExp(`(^|[^\\w.$])${name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\s*\\.`)
    return exprs.some((e) => re.test(e))
  }
  return new Set(spec.relations.filter((r) => r.lookupOnly && !used.has(r.id) && !named(r.id) && !named(r.table.name)).map((r) => r.id))
}
