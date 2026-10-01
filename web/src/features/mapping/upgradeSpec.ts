import type { MappingSpec, MappingView } from './types'

/** A spec as it may arrive: v2, or 1.0 (no specVersion, embeds[] and sources {embed}). */
type AnySpec = Partial<MappingSpec> & { specVersion?: number; embeds?: MappingSpec['relations'] | null; [k: string]: unknown }
type AnySource = { relation?: string; embed?: string; column: string; from?: 'via' }

/**
 * The TypeScript mirror of the server's UpgradeV1 (internal/mapping/upgrade.go, OC-037): each 1.0
 * embed becomes a relation of the mapped table (parentId null) with the same fields, and
 * sources {embed} become {relation}. The server always answers v2; this covers a 1.0 response
 * still in a cache. Idempotent; both run the same fixtures (testdata/specs).
 */
export function upgradeSpec(input: MappingSpec | AnySpec): MappingSpec {
  const spec = input as AnySpec
  const v1 = !spec.specVersion || spec.specVersion < 2
  const { embeds, ...rest } = spec
  const relations = (v1 && !(spec.relations && spec.relations.length) ? (embeds ?? []) : (spec.relations ?? [])).map((r) => (v1 ? { ...r, parentId: null } : r))
  const fieldMappings = (spec.fieldMappings ?? []).map((m) => ({
    ...m,
    sources: (m.sources ?? []).map((s: AnySource) => {
      const { embed, relation, ...src } = s
      const rel = relation ?? embed
      return rel ? { relation: rel, ...src } : src
    }),
    transforms: m.transforms ?? [],
  }))
  return {
    ...rest,
    specVersion: 2,
    mode: spec.mode ?? 'canvas',
    fieldMappings,
    relations,
    aggregates: spec.aggregates ?? [],
    ignoredColumns: spec.ignoredColumns ?? [],
    nullHandling: spec.nullHandling ?? 'include',
  } as MappingSpec
}

/** A mapping view with its spec upgraded (see upgradeSpec). */
export function upgradeView<T extends Pick<MappingView, 'spec'>>(view: T): T {
  return { ...view, spec: upgradeSpec(view.spec) }
}
