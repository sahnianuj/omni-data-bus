import { srcKey } from './embeds'
import type { MappingIssue, MappingSpec } from './types'

export type EdgeState = 'auto' | 'suggested' | 'manual' | 'error'

export type Edge = {
  id: string
  mappingId: string
  from: string // source column (srcKey: "embedId:column" for an embedded column)
  to: string // target field id (or the collapsed ancestor it bundles onto)
  state: EdgeState
  transformCount: number
  firstOp?: string
  /** How many mappings this edge stands for when bundled onto a collapsed object. */
  bundle: number
  /** Multi-source mappings draw several edges converging into a junction. */
  multi: boolean
}

/**
 * One edge per source reference of every non-rejected mapping (OC-019 AC1). Edges into a
 * collapsed object bundle onto its row: one edge per source column with the mapping count.
 * parentOf maps a field id to its parent object id (for bundling).
 */
export function buildEdges(spec: MappingSpec, collapsed: ReadonlySet<string>, parentOf: ReadonlyMap<string, string>, issues: MappingIssue[] = []): Edge[] {
  const errored = new Set(issues.filter((i) => i.severity === 'error' && i.fieldMappingId).map((i) => i.fieldMappingId))
  const visibleTarget = (id: string): string => {
    let outer = id
    for (let p = parentOf.get(id); p; p = parentOf.get(p)) if (collapsed.has(p)) outer = p
    return outer
  }
  const edges: Edge[] = []
  const bundles = new Map<string, Edge>()
  for (const m of spec.fieldMappings) {
    if (m.state === 'rejected') continue
    const state: EdgeState = errored.has(m.id) ? 'error' : m.state === 'pending' ? 'suggested' : m.origin === 'auto' ? 'auto' : 'manual'
    const to = visibleTarget(m.targetFieldId)
    for (const s of m.sources) {
      const from = srcKey(s)
      if (to !== m.targetFieldId) {
        const key = `${from}→${to}`
        const b = bundles.get(key)
        if (b) {
          b.bundle++
          continue
        }
        const e: Edge = { id: `bundle:${key}`, mappingId: m.id, from, to, state, transformCount: 0, bundle: 1, multi: false }
        bundles.set(key, e)
        edges.push(e)
        continue
      }
      edges.push({
        id: `${m.id}:${from}`, mappingId: m.id, from, to, state,
        transformCount: m.transforms.length, firstOp: m.transforms[0]?.op, bundle: 1, multi: m.sources.length > 1,
      })
    }
  }
  return edges
}

/** field id → parent object id, for bundling. */
export function parentMap(spec: MappingSpec): Map<string, string> {
  const out = new Map<string, string>()
  const visit = (f: MappingSpec['docSchema']['root']) => {
    for (const c of f.children ?? []) {
      if (f.id !== spec.docSchema.root.id) out.set(c.id, f.id)
      visit(c)
    }
    // An array's item fields bundle onto the array (its item object has no row).
    for (const c of f.items?.children ?? []) {
      out.set(c.id, f.id)
      visit(c)
    }
  }
  visit(spec.docSchema.root)
  return out
}
