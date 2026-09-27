import { leaves } from '@/features/docschema/docschemaOps'
import { srcKey } from './embeds'
import type { MappingSpec, MappingStats } from './types'

/**
 * Live mapping score; mirrors mapping.ComputeStatsWith (parity: computeStats.test.ts). Columns
 * of embedded tables (embed id → names) are counted in the separate embedded group.
 */
export function computeStats(spec: MappingSpec, columns: string[], embedded?: Record<string, string[]>): MappingStats {
  const auto = new Set<string>()
  const manual = new Set<string>()
  const suggested = new Set<string>()
  const mappedTargets = new Set<string>()
  let confSum = 0
  let confN = 0
  for (const m of spec.fieldMappings) {
    if (m.state === 'active') {
      mappedTargets.add(m.targetFieldId)
      for (const s of m.sources) (m.origin === 'auto' ? auto : manual).add(srcKey(s))
      if (m.origin === 'auto') {
        confSum += m.confidence ?? 0
        confN++
      }
    } else if (m.state === 'pending') {
      for (const s of m.sources) suggested.add(srcKey(s))
    }
  }
  const ignored = new Set(spec.ignoredColumns)
  const st: MappingStats = {
    totalSourceColumns: 0, autoMapped: 0, suggested: 0, manuallyMapped: 0, unmappedSource: 0, ignored: 0,
    totalTargetLeaves: 0, mappedTargetLeaves: 0, requiredTargetUnmapped: 0, autoMapRatio: 0, coverageRatio: 0, averageConfidence: 0,
  }
  const mapped = (k: string) => manual.has(k) || auto.has(k) || suggested.has(k)
  // The parent's join columns of its many-to-one embeds: an unmapped one is carried by the object.
  const carried = new Set(spec.embeds.filter((e) => e.kind === 'many-to-one').flatMap((e) => e.join.map((j) => j.parent)))
  for (const c of columns) {
    if (ignored.has(c)) {
      st.ignored++
      continue
    }
    if (carried.has(c) && !mapped(c)) continue // an unmapped foreign key whose row is embedded
    st.totalSourceColumns++
    if (manual.has(c)) st.manuallyMapped++
    else if (auto.has(c)) st.autoMapped++
    else if (suggested.has(c)) st.suggested++
    else st.unmappedSource++
  }
  for (const l of leaves(spec.docSchema)) {
    st.totalTargetLeaves++
    if (mappedTargets.has(l.fieldId)) st.mappedTargetLeaves++
    else if (l.required) st.requiredTargetUnmapped++
  }
  if (st.totalSourceColumns > 0) {
    st.autoMapRatio = st.autoMapped / st.totalSourceColumns
    st.coverageRatio = (st.autoMapped + st.manuallyMapped) / st.totalSourceColumns
  }
  if (confN > 0) st.averageConfidence = confSum / confN
  if (spec.embeds.length > 0 && embedded) {
    const e = { tables: spec.embeds.length, totalColumns: 0, autoMapped: 0, manuallyMapped: 0, suggested: 0, unmappedSource: 0 }
    for (const emb of spec.embeds) {
      const joins = new Set(emb.join.map((j) => j.child))
      for (const c of embedded[emb.id] ?? []) {
        const k = srcKey({ column: c, embed: emb.id })
        if (joins.has(c) && !mapped(k)) continue // an unmapped join column is the relation itself, not data to map
        e.totalColumns++
        if (manual.has(k)) e.manuallyMapped++
        else if (auto.has(k)) e.autoMapped++
        else if (suggested.has(k)) e.suggested++
        else e.unmappedSource++
      }
    }
    st.embedded = e
  }
  return st
}
