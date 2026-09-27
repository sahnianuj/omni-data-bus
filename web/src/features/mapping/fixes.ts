import { pathsById, setRequired } from '@/features/docschema/docschemaOps'
import { acceptAll, activeFor } from './mappingOps'
import type { MappingIssue, MappingSpec, TransformStep } from './types'
import type { Selection } from './store'

/**
 * Applies a validation issue's one-click fix to the spec (OC-022 AC2).
 * `enable-create` changes the target, not the spec, so the caller handles it.
 */
export function applyFix(spec: MappingSpec, issue: MappingIssue): MappingSpec {
  switch (issue.fix?.kind) {
    case 'mark-optional':
      return issue.fieldId ? { ...spec, docSchema: setRequired(spec.docSchema, issue.fieldId, false) } : spec
    case 'accept-suggestions':
      return acceptAll(spec)
    case 'add-transform': {
      const step = issue.fix.step as TransformStep | undefined
      if (!step || !issue.fieldMappingId) return spec
      return {
        ...spec,
        fieldMappings: spec.fieldMappings.map((m) => (m.id === issue.fieldMappingId ? { ...m, transforms: [step, ...m.transforms] } : m)),
      }
    }
    default:
      return spec
  }
}

/** What *Go to* selects for an issue: its field mapping, else its field. */
export function issueTarget(issue: MappingIssue): Selection {
  if (issue.fieldMappingId) return { kind: 'mapping', id: issue.fieldMappingId }
  if (issue.fieldId) return { kind: 'field', id: issue.fieldId }
  return null
}

/** What *Jump to mapping* selects for a preview row error's dotted field path. */
export function selectionForPath(spec: MappingSpec, path: string | undefined): Selection {
  if (!path) return null
  for (const [id, p] of pathsById(spec.docSchema)) {
    if (p !== path) continue
    const m = activeFor(spec, id)
    return m ? { kind: 'mapping', id: m.id } : { kind: 'field', id }
  }
  return null
}

export const severityOrder = ['error', 'warning', 'info'] as const

export function groupIssues(issues: MappingIssue[]): Record<(typeof severityOrder)[number], MappingIssue[]> {
  const out = { error: [] as MappingIssue[], warning: [] as MappingIssue[], info: [] as MappingIssue[] }
  for (const i of issues) out[i.severity].push(i)
  return out
}
