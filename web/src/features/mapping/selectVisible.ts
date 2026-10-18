import type { Leaf } from '@/features/docschema/docschemaOps'
import { mappingReads } from './reads'
import type { MappingIssue, MappingSpec, SourceColumn } from './types'

export type Filter = 'all' | 'unmapped' | 'suggested' | 'auto' | 'manual' | 'errors' | 'required'

export type SourceState = 'auto' | 'suggested' | 'manual' | 'unmapped' | 'ignored'

/** How each source column is mapped (same precedence as computeStats). */
export function sourceStates(spec: MappingSpec, columns: SourceColumn[]): Map<string, SourceState> {
  const out = new Map<string, SourceState>()
  const ignored = new Set(spec.ignoredColumns)
  for (const c of columns) out.set(c.name, ignored.has(c.name) ? 'ignored' : 'unmapped')
  const rank: Record<SourceState, number> = { ignored: 4, manual: 3, auto: 2, suggested: 1, unmapped: 0 }
  // Columns an expression reads count too: concat(row.first_name, …) uses first_name.
  const reads = mappingReads(spec, new Set(out.keys()))
  for (const m of spec.fieldMappings) {
    if (m.state === 'rejected') continue
    const s: SourceState = m.state === 'pending' ? 'suggested' : m.origin === 'auto' ? 'auto' : 'manual'
    for (const k of reads(m)) {
      const cur = out.get(k)
      if (cur !== undefined && rank[s] > rank[cur]) out.set(k, s)
    }
  }
  return out
}

export type TargetState = 'auto' | 'suggested' | 'manual' | 'unmapped'

export function targetStates(spec: MappingSpec, leaves: Leaf[]): Map<string, TargetState> {
  const out = new Map<string, TargetState>(leaves.map((l) => [l.fieldId, 'unmapped']))
  for (const m of spec.fieldMappings) {
    if (!out.has(m.targetFieldId) || m.state === 'rejected') continue
    if (m.state === 'active') out.set(m.targetFieldId, m.origin === 'auto' ? 'auto' : 'manual')
    else if (out.get(m.targetFieldId) === 'unmapped') out.set(m.targetFieldId, 'suggested')
  }
  return out
}

/**
 * The rows the canvas and table view show (OC-019 AC6): the filter and the search combine,
 * and a search keeps the counterparts of matching rows so their edges stay visible.
 */
export function selectVisible(spec: MappingSpec, columns: SourceColumn[], leaves: Leaf[], filter: Filter, search: string, issues: MappingIssue[] = []) {
  const ss = sourceStates(spec, columns)
  const ts = targetStates(spec, leaves)
  const errFields = new Set(issues.filter((i) => i.severity === 'error').map((i) => i.fieldId).filter(Boolean) as string[])
  const reads = mappingReads(spec, new Set(columns.map((c) => c.name)))
  const errCols = new Set<string>()
  for (const m of spec.fieldMappings) if (errFields.has(m.targetFieldId)) reads(m).forEach((k) => errCols.add(k))
  const q = search.trim().toLowerCase()

  const srcOk = (c: SourceColumn) => {
    const st = ss.get(c.name)!
    switch (filter) {
      case 'all':
        return true
      case 'errors':
        return errCols.has(c.name)
      case 'required':
        return false
      default:
        return st === filter
    }
  }
  const tgtOk = (l: Leaf) => {
    const st = ts.get(l.fieldId)!
    switch (filter) {
      case 'all':
        return true
      case 'errors':
        return errFields.has(l.fieldId)
      case 'required':
        return l.required
      default:
        return st === filter
    }
  }
  let sources = columns.filter(srcOk)
  let targets = leaves.filter(tgtOk)
  if (q) {
    const srcMatch = new Set(sources.filter((c) => c.name.toLowerCase().includes(q)).map((c) => c.name))
    const tgtMatch = new Set(targets.filter((l) => l.path.toLowerCase().includes(q)).map((l) => l.fieldId))
    const keepSrc = new Set(srcMatch)
    const keepTgt = new Set(tgtMatch)
    // Keep the other end of every mapping that touches a match, so its edge stays visible.
    for (const m of spec.fieldMappings) {
      if (m.state === 'rejected') continue
      const ks = reads(m)
      if (ks.some((k) => srcMatch.has(k))) keepTgt.add(m.targetFieldId)
      if (tgtMatch.has(m.targetFieldId)) ks.forEach((k) => keepSrc.add(k))
    }
    sources = sources.filter((c) => keepSrc.has(c.name))
    targets = targets.filter((l) => keepTgt.has(l.fieldId))
  }
  return { sources: sources.map((c) => c.name), targets: targets.map((l) => l.fieldId), sourceStates: ss, targetStates: ts }
}
