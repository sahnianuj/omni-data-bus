import { useEffect, useMemo, useState } from 'react'
import { valuesAt } from '@/features/computed/ComputedEditor'
import { pathsById } from '@/features/docschema/docschemaOps'
import { previewMapping } from '@/features/mapping/api'
import type { MappingSpec } from '@/features/mapping/types'
import { ApiError } from '@/lib/api/client'
import { setExpression } from './composerOps'

const SAMPLES = 5
const CHECK_DEBOUNCE_MS = 400
const HELPERS = 'upper, lower, trim, len, round, coalesce, now, concat'

/** What a composer expression being typed does: compiles (with the first sample's value) or why not. */
export type ExpressionCheck = { state: 'idle' | 'checking' } | { state: 'ok'; value?: unknown } | { state: 'error'; message: string }

/**
 * What to write instead, for names people bring from templates and other languages: $doc is
 * the template's root, expressions read the row as $root; string() and friends are concat.
 */
export function expressionHint(message: string): string {
  const name = /unknown name (\S+)/.exec(message)?.[1]
  if (!name) return message
  if (name.startsWith('$')) return `${message}; the document's own row is $root (as in $root.id) and the enclosing row $parent`
  if (/^(string|str|toString|int|float|format|sprintf|join|substr|replace)$/.test(name)) return `${message}; the functions are ${HELPERS} (concat("a", 1) joins text with numbers)`
  return message
}

/**
 * Checks an expression as typed against the server's compiler (OC-047), the way the template
 * editor previews (OC-138): the same preview a run would show, with the text in place of the
 * field's mapping. Only this field's problems count; another field's leave it unchecked.
 */
export function useExpressionCheck(projectId: string, mappingId: string, spec: MappingSpec, fieldId: string, text: string, enabled: boolean): ExpressionCheck {
  // The last answer, with the draft it's for: a newer draft is "checking" until its own comes.
  const [result, setResult] = useState<{ for: MappingSpec; check: ExpressionCheck } | null>(null)
  const expr = text.trim()
  const override = useMemo(() => (enabled && expr ? setExpression(spec, fieldId, expr) : null), [enabled, expr, spec, fieldId])
  useEffect(() => {
    if (!override) return
    const path = pathsById(override.docSchema).get(fieldId) ?? ''
    let current = true
    const done = (check: ExpressionCheck) => current && setResult({ for: override, check })
    const timer = setTimeout(() => {
      previewMapping(projectId, mappingId, { limit: SAMPLES, specOverride: override })
        .then((r) => {
          const items = r.items ?? []
          const failed = items.find((i) => i.error && i.error.fieldPath === path)
          if (failed?.error) return done({ state: 'error', message: expressionHint(failed.error.message) })
          const doc = items.find((i) => i.document)?.document
          done({ state: 'ok', value: doc ? valuesAt(doc, path)[0] : undefined })
        })
        .catch((e: unknown) => {
          const mine = e instanceof ApiError ? e.fieldErrors.find((f) => f.field === path) : undefined
          done(mine ? { state: 'error', message: expressionHint(mine.message) } : { state: 'idle' })
        })
    }, CHECK_DEBOUNCE_MS)
    return () => {
      current = false
      clearTimeout(timer)
    }
  }, [override, projectId, mappingId, fieldId])
  if (!override) return { state: 'idle' }
  return result?.for === override ? result.check : { state: 'checking' }
}
