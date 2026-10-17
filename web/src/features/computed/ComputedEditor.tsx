import { ChevronLeft, ChevronRight } from 'lucide-react'
import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { pathsById } from '@/features/docschema/docschemaOps'
import { previewMapping } from '@/features/mapping/api'
import { fieldScopes } from '@/features/mapping/embeds'
import { setComputed } from '@/features/mapping/mappingOps'
import { useWorkbench } from '@/features/mapping/store'
import type { ComputedRule, MappingPreview } from '@/features/mapping/types'
import { cn } from '@/lib/utils'
import { completionsAt, type Completion } from './completions'
import { usesAndUsedBy } from './refs'
import { parseTemplate } from './template'

const SAMPLES = 20
const LIVE_DEBOUNCE_MS = 300
const COMMIT_DEBOUNCE_MS = 500

/** The values at a path such as items[].label in a document (each item's, for arrays). */
export function valuesAt(doc: unknown, path: string): unknown[] {
  let cur: unknown[] = [doc]
  for (const part of path.split('.')) {
    const many = part.endsWith('[]')
    const name = many ? part.slice(0, -2) : part
    cur = cur.flatMap((v) => {
      if (!v || typeof v !== 'object' || Array.isArray(v) || !(name in v)) return []
      const x = (v as Record<string, unknown>)[name]
      return many ? (Array.isArray(x) ? x : []) : [x]
    })
  }
  return cur
}

const show = (v: unknown) => (v === undefined ? '—' : JSON.stringify(v))

/**
 * The template editor of a computed field (OC-138): the text with completions of the names and
 * fields in scope, the server's diagnostics, "null if any value is missing", and the value for
 * the sample documents, stepping through them.
 */
export function ComputedEditor({ fieldId }: { fieldId: string }) {
  const { t } = useTranslation()
  const view = useWorkbench((s) => s.view)
  const spec = useWorkbench((s) => s.history.present)
  const columns = useWorkbench((s) => s.columns)
  const issues = useWorkbench((s) => s.issues)
  const apply = useWorkbench((s) => s.apply)
  const select = useWorkbench((s) => s.select)
  const rule = spec.fieldMappings.find((m) => m.targetFieldId === fieldId && m.state === 'active')?.computed
  const [text, setText] = useState(rule?.expr ?? '')
  const [caret, setCaret] = useState(0)
  const [active, setActive] = useState(0)
  const [dismissed, setDismissed] = useState(false)
  const ref = useRef<HTMLTextAreaElement>(null)
  const listId = useId()
  const path = pathsById(spec.docSchema).get(fieldId) ?? ''
  const rootScope = (fieldScopes(spec).get(fieldId) ?? '') === ''

  // The stored rule follows the text after a pause: one undo step per edit, not per key. (Callers
  // key the editor by field, so another field starts from its own text.)
  const commit = (next: Partial<ComputedRule>) => {
    if (!rule) return
    apply((s) => setComputed(s, fieldId, { ...rule, ...next }))
  }
  const stored = rule?.expr
  useEffect(() => {
    if (stored === undefined || text === stored) return
    const timer = setTimeout(() => apply((s) => setComputed(s, fieldId, { ...(s.fieldMappings.find((m) => m.targetFieldId === fieldId && m.state === 'active')?.computed ?? { lang: 'template' }), expr: text })), COMMIT_DEBOUNCE_MS)
    return () => clearTimeout(timer)
  }, [text, stored, apply, fieldId])

  const completion = dismissed ? null : completionsAt(spec, fieldId, text, caret, columns.map((c) => c.name), rootScope)
  const options: Completion[] = completion?.items ?? []
  const open = options.length > 0
  const accept = (c: Completion) => {
    if (!completion) return
    const next = text.slice(0, completion.from) + c.insert + text.slice(caret)
    const pos = completion.from + c.insert.length
    setText(next)
    setCaret(pos)
    requestAnimationFrame(() => ref.current?.setSelectionRange(pos, pos))
  }
  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (!open) return
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      setActive((a) => (a + (e.key === 'ArrowDown' ? 1 : options.length - 1)) % options.length)
    } else if (e.key === 'Enter' || e.key === 'Tab') {
      e.preventDefault()
      accept(options[Math.min(active, options.length - 1)]!)
    } else if (e.key === 'Escape') {
      e.preventDefault()
      setDismissed(true)
    }
  }

  // Diagnostics: the client's syntax check at once, the server's compile issues after saving.
  const syntax = parseTemplate(text).error
  const serverIssues = issues.filter((i) => i.fieldId === fieldId && (i.code.startsWith('COMPUTED_') || i.code === 'TYPE_MISMATCH'))

  // The value for the sample documents, with the text as typed.
  const [preview, setPreview] = useState<MappingPreview | null>(null)
  const [index, setIndex] = useState(0)
  const override = useMemo(() => (rule ? setComputed(spec, fieldId, { ...rule, expr: text }) : spec), [spec, fieldId, rule, text])
  useEffect(() => {
    if (!rule || syntax) return
    let current = true
    const timer = setTimeout(() => {
      previewMapping(view.projectId, view.id, { limit: SAMPLES, specOverride: override })
        .then((r) => current && setPreview(r))
        .catch(() => current && setPreview(null))
    }, LIVE_DEBOUNCE_MS)
    return () => {
      current = false
      clearTimeout(timer)
    }
  }, [override, view.projectId, view.id, rule, syntax])
  const items = preview?.items ?? []
  const item = items[Math.min(index, Math.max(items.length - 1, 0))]
  const values = item?.document ? valuesAt(item.document, path) : []
  const rowError = item?.error && item.error.fieldPath === path ? item.error : undefined

  const links = usesAndUsedBy(spec, fieldId)
  const names = pathsById(spec.docSchema)
  if (!rule) return null
  return (
    <div className="flex flex-col gap-2">
      <div className="relative">
        <textarea
          ref={ref}
          aria-label={t('computed.template')}
          aria-controls={open ? listId : undefined}
          aria-autocomplete="list"
          aria-activedescendant={open ? `${listId}-${Math.min(active, options.length - 1)}` : undefined}
          value={text}
          spellCheck={false}
          rows={2}
          placeholder="${doc.firstName} ${doc.lastName}"
          onChange={(e) => {
            setText(e.target.value)
            setCaret(e.target.selectionStart)
            setActive(0)
            setDismissed(false)
          }}
          onSelect={(e) => setCaret(e.currentTarget.selectionStart)}
          onKeyDown={onKeyDown}
          onBlur={() => text !== rule.expr && commit({ expr: text })}
          className="w-full resize-y rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 py-1.5 font-mono text-xs"
        />
        {open ? (
          <ul id={listId} role="listbox" aria-label={t('computed.suggestions')} className="absolute left-0 top-full z-20 mt-1 max-h-48 w-72 overflow-y-auto rounded-md border border-border bg-surface py-1 shadow-md">
            {options.map((o, i) => (
              <li
                key={o.label}
                id={`${listId}-${i}`}
                role="option"
                aria-selected={i === active}
                onMouseDown={(e) => {
                  e.preventDefault()
                  accept(o)
                }}
                className={cn('flex cursor-pointer items-center justify-between gap-3 px-2 py-1 font-mono text-xs', i === active && 'bg-primary-soft')}
              >
                <span>{o.label}</span>
                <span className="text-[11px] text-subtle">{o.detail}</span>
              </li>
            ))}
          </ul>
        ) : null}
      </div>
      {syntax ? (
        <p role="alert" className="text-xs text-danger">{syntax.message}</p>
      ) : (
        serverIssues.map((i, n) => (
          <p key={n} role="alert" className="text-xs text-danger">
            {i.message}
            {i.line ? <span className="text-subtle"> {t('computed.at', { line: i.line, column: i.column })}</span> : null}
          </p>
        ))
      )}
      {links.uses.length + links.usedBy.length > 0 ? (
        <p className="flex flex-wrap items-center gap-x-1 text-xs text-muted">
          {(['uses', 'usedBy'] as const).map((k) =>
            links[k].length === 0 ? null : (
              <span key={k} className="flex flex-wrap items-center gap-1">
                {k === 'usedBy' && links.uses.length > 0 ? <span aria-hidden>·</span> : null}
                {t(`computed.${k}`)}
                {links[k].map((id, i) => (
                  <button key={id} type="button" onClick={() => select({ kind: 'field', id })} className="font-mono text-primary-soft-foreground hover:underline">
                    {names.get(id) ?? id}
                    {i < links[k].length - 1 ? ',' : ''}
                  </button>
                ))}
              </span>
            ),
          )}
        </p>
      ) : null}
      <label className="flex items-center gap-2 text-xs text-muted">
        <input type="checkbox" checked={!!rule.nullIfAnyMissing} onChange={(e) => commit({ nullIfAnyMissing: e.target.checked })} />
        {t('computed.nullIfAnyMissing')}
      </label>
      <section aria-label={t('computed.result')} className="flex items-center gap-2 text-xs">
        <button type="button" aria-label={t('composer.sample.prev')} disabled={index === 0} onClick={() => setIndex((i) => i - 1)} className="rounded p-0.5 hover:bg-surface-muted disabled:opacity-40">
          <ChevronLeft className="size-3.5" aria-hidden />
        </button>
        <button type="button" aria-label={t('composer.sample.next')} disabled={index >= items.length - 1} onClick={() => setIndex((i) => i + 1)} className="rounded p-0.5 hover:bg-surface-muted disabled:opacity-40">
          <ChevronRight className="size-3.5" aria-hidden />
        </button>
        {rowError ? (
          <span className="text-danger">{rowError.message}</span>
        ) : item ? (
          <span className="min-w-0 truncate font-mono">
            → {values.length === 0 ? show(undefined) : values.slice(0, 5).map(show).join(', ')}
            {values.length > 5 ? <span className="text-subtle"> {t('computed.more', { n: values.length - 5 })}</span> : null}
          </span>
        ) : (
          <span className="text-subtle">{t('computed.noSample')}</span>
        )}
      </section>
    </div>
  )
}
