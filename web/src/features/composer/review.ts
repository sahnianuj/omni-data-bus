import type { Proposal, ProposalItem } from '@/features/mapping/api'
import type { MappingSpec } from '@/features/mapping/types'
import { setVariantRule, unbind } from './composerOps'

// Review mode (OC-168): a proposal (OC-163) is applied to the spec as one undo step and the
// user fine-tunes it with the composer's own editors. These are the proposal's per-node items
// next to that spec: what each row shows, what "needs review" means, and how a row is taken
// back. Apply keeps everything as one step; Discard goes back to before the proposal.

export type ReviewFilter = 'all' | 'review' | 'unresolved' | 'notPopulated' | 'edited'

export type Review = {
  proposal: Proposal
  /** history.past.length before the proposal was applied (squash on Apply, revert on Discard). */
  mark: number
}

const settled = new Set(['alreadyMapped', 'notPopulated'])

/** A row that's worth a look: not high confidence, or nothing fills it (n / p stop on both). */
export function needsReview(it: ProposalItem): boolean {
  if (it.status === 'unresolved') return true
  return uncertain(it)
}

/** Proposed, but below high confidence: the banner's and filter's "needs review". */
export function uncertain(it: ProposalItem): boolean {
  return !settled.has(it.status) && it.status !== 'unresolved' && it.band !== 'high'
}

/** The item for each node or field, by field id. */
export function itemsById(review: Review | null): Map<string, ProposalItem> {
  return new Map((review?.proposal.items ?? []).map((it) => [it.fieldId, it]))
}

/**
 * Whether the user changed what the proposal put on a row: another source, transform or
 * table, or took it back.
 */
export function edited(spec: MappingSpec, review: Review, it: ProposalItem): boolean {
  const proposed = review.proposal.spec
  if (it.kind === 'binding') {
    const want = it.relationIds?.[0]
    if (!want) return false
    const then = proposed.relations.find((r) => r.id === want)
    if (then && then.targetFieldId !== it.fieldId) {
      // A variant of the array's relation (OC-171): its rule is what was proposed.
      const rule = (r: typeof then | undefined) => JSON.stringify(r?.variants?.find((v) => v.fieldId === it.fieldId) ?? null)
      return rule(spec.relations.find((r) => r.id === want)) !== rule(then)
    }
    const now = spec.relations.find((r) => !r.lookupOnly && r.targetFieldId === it.fieldId)
    return !now || !then || JSON.stringify({ ...now, id: '' }) !== JSON.stringify({ ...then, id: '' })
  }
  const then = proposed.fieldMappings.filter((m) => (it.mappingIds ?? []).includes(m.id))
  const now = spec.fieldMappings.filter((m) => m.targetFieldId === it.fieldId && m.state !== 'rejected')
  const strip = (m: (typeof now)[number]) => JSON.stringify({ sources: m.sources, transforms: m.transforms, state: m.state })
  if ((it.aggregateIds ?? []).length > 0) {
    const a = (spec.aggregates ?? []).find((x) => x.targetFieldId === it.fieldId)
    return !a || !(it.aggregateIds ?? []).includes(a.id)
  }
  if (then.length === 0) return now.length > 0 // unresolved, then filled by the user
  return now.length !== then.length || now.some((m, i) => strip(m) !== strip(then[i]!))
}

/** Takes one row's proposal back: unbinds a node (and what's under it), or removes a field's mapping. */
export function removeItem(spec: MappingSpec, it: ProposalItem): MappingSpec {
  if (it.kind === 'binding') {
    const rel = spec.relations.find((r) => r.id === it.relationIds?.[0])
    // A variant of the array's relation: only its rule goes (OC-171).
    if (rel && rel.targetFieldId !== it.fieldId) return setVariantRule(spec, rel.id, it.fieldId, null)
    return it.relationIds?.length ? unbind(spec, it.fieldId) : spec
  }
  const mappings = new Set(it.mappingIds ?? [])
  const aggregates = new Set(it.aggregateIds ?? [])
  const next: MappingSpec = {
    ...spec,
    fieldMappings: spec.fieldMappings.filter((m) => !mappings.has(m.id)),
    aggregates: (spec.aggregates ?? []).filter((a) => !aggregates.has(a.id)),
  }
  // The lookups and summary relations it added go too, unless something else still reads them.
  const used = new Set<string>()
  for (const m of next.fieldMappings) for (const s of m.sources) if (s.relation) used.add(s.relation)
  for (const a of next.aggregates ?? []) used.add(a.relationId)
  for (const m of next.fieldMappings) for (const t of m.transforms) if (t.op === 'expression') for (const r of next.relations) if (String(t.args?.expr ?? '').includes(r.table.name + '.')) used.add(r.id)
  const drop = new Set((it.relationIds ?? []).filter((id) => !used.has(id) && !next.relations.some((r) => r.id === id && !r.lookupOnly && r.targetFieldId)))
  return drop.size ? { ...next, relations: next.relations.filter((r) => !drop.has(r.id)) } : next
}

/** "Apply high only": takes back every row below high confidence, containers first. */
export function keepHighOnly(spec: MappingSpec, review: Review): MappingSpec {
  let next = spec
  const items = review.proposal.items.filter((it) => !settled.has(it.status) && it.status !== 'unresolved' && it.band !== 'high')
  for (const it of items.filter((x) => x.kind === 'binding')) next = removeItem(next, it)
  for (const it of items.filter((x) => x.kind === 'field')) next = removeItem(next, it)
  return next
}

export type ReviewCounts = { fields: number; proposed: number; review: number; notPopulated: number; unresolved: number; edited: number }

export function counts(spec: MappingSpec, review: Review): ReviewCounts {
  const out: ReviewCounts = { fields: 0, proposed: 0, review: 0, notPopulated: 0, unresolved: 0, edited: 0 }
  for (const it of review.proposal.items) {
    const ed = edited(spec, review, it)
    if (ed) out.edited++
    if (it.kind !== 'field') continue
    out.fields++
    if (it.status === 'notPopulated') out.notPopulated++
    else if (it.status === 'unresolved' && !ed) out.unresolved++
    else out.proposed++
    if (uncertain(it) && !ed) out.review++
  }
  return out
}

/** Whether a row shows under a filter. */
export function matches(spec: MappingSpec, review: Review, it: ProposalItem | undefined, f: ReviewFilter): boolean {
  if (f === 'all') return true
  if (!it) return false
  switch (f) {
    case 'review':
      return uncertain(it) && !edited(spec, review, it)
    case 'unresolved':
      return it.status === 'unresolved' && !edited(spec, review, it)
    case 'notPopulated':
      return it.status === 'notPopulated'
    case 'edited':
      return edited(spec, review, it)
  }
}

/**
 * A recomputed proposal (Propose again, with the current spec as the base): what was already
 * there keeps its original item; new proposals replace theirs.
 */
export function merge(prev: Proposal, next: Proposal): Proposal {
  const old = new Map(prev.items.map((it) => [it.fieldId, it]))
  return { ...next, items: next.items.map((it) => (it.status === 'alreadyMapped' && old.has(it.fieldId) ? old.get(it.fieldId)! : it)) }
}

/** The coverage sheet (OC-169 AC3): one row per design field, with what the proposal says. */
export function coverageCSV(spec: MappingSpec, review: Review): string {
  const esc = (v: unknown) => {
    const s = v == null ? '' : String(v)
    return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s
  }
  const rows = [['path', 'kind', 'status', 'confidence', 'source', 'reason code', 'reasons', 'required']]
  for (const it of review.proposal.items) {
    const status = edited(spec, review, it) ? 'edited' : it.status === 'unresolved' ? `unresolved: ${it.code ?? ''}` : it.status
    rows.push([it.path, it.kind, status, it.band ?? '', it.summary, it.code ?? '', it.reasons.join('; '), it.required ? 'yes' : ''])
  }
  const c = counts(spec, review)
  rows.push([`summary: ${c.proposed} of ${c.fields} fields proposed, ${c.review} need review, ${c.notPopulated} not populated, ${c.unresolved} unresolved`, '', '', '', '', '', '', ''])
  return rows.map((r) => r.map(esc).join(',')).join('\n') + '\n'
}
