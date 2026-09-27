import type { DocSchema } from '@/features/mapping/types'
import { walk } from './docschemaOps'

export type SchemaDiff = { added: string[]; removed: string[]; typeChanged: { path: string; from: string; to: string }[] }

/** What replacing `current` by `next` changes, by path (the replace/merge dialog's preview). */
export function schemaDiff(current: DocSchema, next: DocSchema): SchemaDiff {
  const before = new Map(walk(current).map((r) => [r.path, r.field.type]))
  const after = new Map(walk(next).map((r) => [r.path, r.field.type]))
  const d: SchemaDiff = { added: [], removed: [], typeChanged: [] }
  for (const [p, t] of after) {
    const old = before.get(p)
    if (old === undefined) d.added.push(p)
    else if (old !== t) d.typeChanged.push({ path: p, from: old, to: t })
  }
  for (const p of before.keys()) if (!after.has(p)) d.removed.push(p)
  return d
}
