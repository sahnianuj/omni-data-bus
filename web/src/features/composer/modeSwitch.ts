import type { DocField, MappingSpec } from '@/features/mapping/types'
import { boundRelations } from './scope'
import type { TableRef } from './composerOps'

// Canvas ↔ Composer (OC-048 AC6): both edit the same relation tree (OC-047), so switching only
// changes the mode and the composer's binding annotations; no relation, mapping or transform
// is added or lost.

/** Mirrors mapping.SyncBindings: bound nodes carry their relation's id; stale bindings go. */
export function syncBindings(spec: MappingSpec): MappingSpec {
  const bound = boundRelations(spec)
  const visit = (f: DocField): DocField => {
    const r = bound.get(f.id)
    let next = f
    if (r && f.binding?.relationId !== r.id) next = { ...f, binding: { ...f.binding, relationId: r.id } }
    else if (!r && f.binding) {
      next = { ...f }
      delete next.binding
    }
    const children = next.children?.map(visit)
    const items = next.items ? visit(next.items) : undefined
    const changed = (children && children.some((c, i) => c !== next.children![i])) || items !== next.items
    return changed ? { ...next, children, ...(next.items ? { items } : {}) } : next
  }
  const root = visit(spec.docSchema.root)
  return root === spec.docSchema.root ? spec : { ...spec, docSchema: { ...spec.docSchema, root } }
}

/** Opens a mapping in the composer; it's driven by its own table until the driving table changes. */
export function toComposed(spec: MappingSpec, own?: TableRef): MappingSpec {
  return syncBindings({ ...spec, mode: 'composed', ...(spec.root || !own ? {} : { root: { table: own } }) })
}

const sameTable = (a: TableRef | undefined, b: TableRef) => !!a && a.schema === b.schema && a.name === b.name

/**
 * Opens a composed mapping on the canvas. A canvas mapping is driven by its own table, so a
 * mapping driven by another table stays composed (blocked names the driving table).
 */
export function toCanvas(spec: MappingSpec, own: TableRef): { spec: MappingSpec } | { blocked: TableRef } {
  if (spec.mode === 'composed' && spec.root && !sameTable(spec.root.table, own)) return { blocked: spec.root.table }
  return { spec: { ...spec, mode: 'canvas', root: spec.root ?? { table: own } } }
}
