import { pathsById } from '@/features/docschema/docschemaOps'
import type { HopsOf } from '@/features/mapping/relationsApi'
import { directionOf } from '@/features/mapping/relatedGroups'
import type { JunctionCandidate, MappingSpec, Relation, RelationHop } from '@/features/mapping/types'
import type { TableRef } from './composerOps'

// The Composer's "In this document" (OC-142): one node per relation the mapping already reads
// (a relation instance), nested like the document, and under each, the relationships of its
// table that aren't embedded yet, ready to embed into its object or to look up.

export type Instance = {
  key: string
  /** '' is the driving table. */
  relationId: string
  table: TableRef
  /** Where its rows go: "items[]", "items[].product"; "" for the driving table. */
  path: string
  /** The document node new fields go under (the object, or an array's items); '' when none. */
  containerId: string
  depth: number
  lookup: boolean
  children: Instance[]
}

const key = (t: TableRef) => `${t.schema}.${t.name}`
const sameJoin = (a: { parent: string; child: string }[] | undefined, b: { parent: string; child: string }[]) =>
  (a ?? []).map((j) => `${j.parent}=${j.child}`).join(',') === b.map((j) => `${j.parent}=${j.child}`).join(',')

/** The driving table and every relation under it, as instances. */
export function documentSources(spec: MappingSpec, driving: TableRef): Instance {
  const paths = pathsById(spec.docSchema)
  const container = (r: Relation): string => {
    const find = (f: import('@/features/mapping/types').DocField): string | undefined => {
      if (f.id === r.targetFieldId) return f.type === 'object' ? f.id : f.type === 'array' && f.items?.type === 'object' ? f.items.id : ''
      for (const c of f.children ?? []) {
        const x = find(c)
        if (x !== undefined) return x
      }
      return f.items ? find(f.items) : undefined
    }
    return r.lookupOnly ? '' : (find(spec.docSchema.root) ?? '')
  }
  const build = (rel: Relation | undefined, depth: number): Instance => {
    const id = rel?.id ?? ''
    const children = spec.relations.filter((r) => (r.parentId ?? '') === id && !r.group).map((r) => build(r, depth + 1))
    // A grouped document's rows (OC-063) are part of the document itself: their relations nest here.
    const grouped = spec.relations.filter((r) => (r.parentId ?? '') === id && r.group).flatMap((g) => build(g, depth).children)
    return {
      key: id || 'root',
      relationId: id,
      table: rel?.table ?? driving,
      path: rel ? (rel.lookupOnly ? '' : (paths.get(rel.targetFieldId ?? '') ?? '')) + (rel.kind !== 'many-to-one' && !rel.lookupOnly ? '[]' : '') : '',
      containerId: rel ? container(rel) : spec.docSchema.root.id,
      depth,
      lookup: !!rel?.lookupOnly,
      children: [...children, ...grouped],
    }
  }
  return build(undefined, 0)
}

export type AvailableHop = RelationHop & { direction: 'references' | 'referencedBy'; cardinality: 'one-to-one' | 'many-to-one' | 'one-to-many' }

/**
 * An instance's relationships not yet in the document: its table's hops (OC-141), less those
 * already embedded or looked up under it and the hop back to the relation that embedded it.
 */
export function availableHops(spec: MappingSpec, inst: Instance, hopsOf: HopsOf, junctionsOf: (t: TableRef) => JunctionCandidate[] | undefined): { references: AvailableHop[]; referencedBy: (AvailableHop | JunctionCandidate)[] } | undefined {
  const hops = hopsOf(inst.table)
  if (!hops) return undefined
  const under = spec.relations.filter((r) => (r.parentId ?? '') === inst.relationId || (inst.relationId === '' && r.parentId && spec.relations.find((g) => g.id === r.parentId)?.group))
  const own = spec.relations.find((r) => r.id === inst.relationId)
  const parentTable = own ? (spec.relations.find((r) => r.id === own.parentId)?.table ?? spec.root?.table) : undefined
  const embedded = (h: RelationHop) =>
    under.some((r) => key(r.table) === key(h.to) && sameJoin(r.join, h.join)) ||
    // The way back to the table that embedded this one.
    (!!own && !!parentTable && key(h.to) === key(parentTable) && sameJoin(own.join, h.join.map((j) => ({ parent: j.child, child: j.parent }))))
  const out = { references: [] as AvailableHop[], referencedBy: [] as (AvailableHop | JunctionCandidate)[] }
  for (const h of hops) {
    if (embedded(h)) continue
    const d = directionOf(h)
    const hop = { ...h, ...d }
    if (d.direction === 'references') out.references.push(hop)
    else out.referencedBy.push(hop)
  }
  if (inst.depth === 0) {
    for (const j of junctionsOf(inst.table) ?? []) {
      if (!under.some((r) => r.kind === 'many-to-many' && key(r.table) === key(j.table) && r.via?.table.name === j.via.name)) out.referencedBy.push(j)
    }
  }
  return out
}

/** Every instance, depth first. */
export function flatten(root: Instance): Instance[] {
  return [root, ...root.children.flatMap(flatten)]
}
