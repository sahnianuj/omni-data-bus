import { describe, expect, it } from 'vitest'
import type { Proposal } from '@/features/mapping/api'
import type { MappingSpec } from '@/features/mapping/types'
import proposalJSON from '../../../testdata/design-first/cart-proposal.json'
import { setConstant } from '@/features/mapping/mappingOps'
import { counts, coverageCSV, edited, itemsById, keepHighOnly, matches, merge, needsReview, removeItem, type Review } from './review'

const proposal = proposalJSON as unknown as Proposal
const review: Review = { proposal, mark: 0 }
const byPath = new Map(proposal.items.map((it) => [it.path, it]))
const item = (p: string) => byPath.get(p)!

describe('review (OC-168)', () => {
  it('counts what the banner shows', () => {
    const c = counts(proposal.spec, review)
    expect(c.fields).toBe(proposal.coverage.fields)
    expect(c.notPopulated).toBe(proposal.coverage.notPopulated)
    expect(c.unresolved).toBe(proposal.coverage.unresolved)
    expect(c.review).toBe(proposal.coverage.needsReview)
    expect(c.edited).toBe(0)
    expect(itemsById(review).get(item('savedAt').fieldId)?.summary).toBe('← saved_orders.saved_at')
  })

  it('an edit shows as edited and leaves needs review', () => {
    const name = item('partner.name')
    expect(needsReview(name)).toBe(true)
    const spec = setConstant(proposal.spec, name.fieldId, 'Partner')
    expect(edited(spec, review, name)).toBe(true)
    expect(matches(spec, review, name, 'edited')).toBe(true)
    expect(matches(spec, review, name, 'review')).toBe(false)
    expect(counts(spec, review).edited).toBe(1)
    expect(edited(proposal.spec, review, item('items[].price'))).toBe(false)
  })

  it('taking a lookup field back removes its lookup only when nothing else reads it', () => {
    const saved = item('savedAt')
    const lookup = saved.relationIds![0]!
    let spec: MappingSpec = removeItem(proposal.spec, saved)
    expect(spec.fieldMappings.some((m) => m.targetFieldId === saved.fieldId)).toBe(false)
    expect(spec.relations.some((r) => r.id === lookup)).toBe(true) // orderCollectionType and status still read it
    spec = removeItem(spec, item('orderCollectionType'))
    spec = removeItem(spec, item('status'))
    expect(spec.relations.some((r) => r.id === lookup)).toBe(false)
  })

  it('taking a binding back unbinds the node and what is under it', () => {
    const items = item('items[]')
    const spec = removeItem(proposal.spec, items)
    expect(spec.relations.some((r) => r.targetFieldId === items.fieldId)).toBe(false)
    expect(spec.fieldMappings.some((m) => m.targetFieldId === item('items[].price').fieldId)).toBe(false)
    expect(spec.relations.some((r) => r.targetFieldId === item('items[].modifiers[]').fieldId)).toBe(false)
  })

  it('apply high only keeps the high rows', () => {
    const spec = keepHighOnly(proposal.spec, review)
    // A medium container (modifiers[] ~ customizations) goes with everything under it.
    const removed = proposal.items.filter((it) => it.kind === 'binding' && it.status === 'bound' && it.band !== 'high').map((it) => it.path)
    expect(removed).toContain('items[].modifiers[]')
    const under = (p: string) => removed.some((r) => p.startsWith(r + '.'))
    for (const it of proposal.items) {
      if (it.kind !== 'field' || !it.mappingIds?.length) continue
      const kept = spec.fieldMappings.some((m) => it.mappingIds!.includes(m.id))
      if (it.band === 'high' && !under(it.path)) expect(kept, it.path).toBe(true)
      else if (it.status !== 'notPopulated' && it.status !== 'alreadyMapped') expect(kept, it.path).toBe(false)
    }
    expect(spec.aggregates ?? []).toHaveLength(0) // the tip total is medium
  })

  it('a recomputed proposal keeps the original items of what was already there', () => {
    const again: Proposal = { ...proposal, items: proposal.items.map((it) => ({ ...it, status: 'alreadyMapped' as const, band: 'high' as const, reasons: ['already mapped'] })) }
    const merged = merge(proposal, again)
    expect(merged.items.find((it) => it.path === 'partner.name')!.band).toBe('medium')
  })

  it('OC-169 AC3: the coverage sheet has a row per field with its status and reason, and the summary', () => {
    const csv = coverageCSV(proposal.spec, review).trim().split('\n')
    expect(csv[0]).toBe('path,kind,status,confidence,source,reason code,reasons,required')
    expect(csv).toHaveLength(proposal.items.length + 2)
    expect(csv.find((l) => l.startsWith('updatedAt,'))).toMatch(/^updatedAt,field,unresolved: NO_COLUMN,low,unresolved,NO_COLUMN,.*,yes$/)
    expect(csv.find((l) => l.startsWith('items[].price,'))).toContain('converted,high,← cart_items.price × 100')
    const c = counts(proposal.spec, review)
    expect(csv.at(-1)).toContain(`${c.proposed} of ${c.fields} fields proposed, ${c.review} need review, ${c.notPopulated} not populated, ${c.unresolved} unresolved`)
    expect(needsReview(item('updatedAt'))).toBe(true)
  })

  it('OC-171: a variant of the array relation is edited by its rule, and taken back alone', async () => {
    const pay = (await import('../../../testdata/design-first/cart-payments-proposal.json')).default as unknown as Proposal
    const r: Review = { proposal: pay, mark: 0 }
    const variants = pay.items.filter((it) => it.kind === 'binding' && it.path.startsWith('payments[]<'))
    expect(variants).toHaveLength(5)
    for (const v of variants) expect(edited(pay.spec, r, v), v.path).toBe(false)
    const scc = variants.find((v) => v.path === 'payments[]<SCC>')!
    const spec = removeItem(pay.spec, scc)
    const rel = spec.relations.find((x) => x.id === scc.relationIds![0])!
    expect(rel.targetFieldId, 'the array stays bound').toBeTruthy()
    expect(rel.variants).toHaveLength(4)
    expect(edited(spec, r, scc)).toBe(true)
    expect(edited(spec, r, variants.find((v) => v.path === 'payments[]<SVC>')!)).toBe(false)
  })
})

