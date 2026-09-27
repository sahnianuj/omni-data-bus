import { useRef } from 'react'
import { useTranslation } from 'react-i18next'
import type { MappingIssue } from './types'

type Props = {
  id: string
  value: string
  onChange: (value: string) => void
  /** Server compile issues for this step; `line`/`column` are 1-based. */
  issues?: MappingIssue[]
  disabled?: boolean
}

/** Offset of a 1-based line/column in text, clamped to its end. */
export function offsetOf(text: string, line: number, column: number): number {
  const lines = text.split('\n')
  let at = 0
  for (let i = 0; i < Math.min(line - 1, lines.length - 1); i++) at += lines[i]!.length + 1
  const here = lines[Math.min(line, lines.length) - 1] ?? ''
  return Math.min(at + Math.max(column - 1, 0), at + here.length)
}

/**
 * Edits an `expression` step. A server diagnostic shows the offending line with a caret under its
 * column; clicking it puts the cursor there (OC-020 AC8).
 */
export function ExpressionEditor({ id, value, onChange, issues = [], disabled }: Props) {
  const { t } = useTranslation()
  const ref = useRef<HTMLTextAreaElement>(null)
  const lines = value.split('\n')
  const jump = (x: MappingIssue) => {
    const el = ref.current
    if (!el || !x.line) return
    const at = offsetOf(value, x.line, x.column ?? 1)
    el.focus()
    el.setSelectionRange(at, at)
  }
  return (
    <div className="flex flex-col gap-1.5">
      <textarea
        ref={ref}
        id={id}
        rows={3}
        spellCheck={false}
        disabled={disabled}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={t('transform.exprHint')}
        aria-invalid={issues.length > 0 || undefined}
        className="rounded-[var(--radius-control)] border border-border-strong bg-surface p-2 font-mono text-xs aria-[invalid]:border-danger"
      />
      {issues.map((x) =>
        x.line ? (
          <button key={`${x.code}:${x.line}:${x.column}:${x.message}`} type="button" role="alert" onClick={() => jump(x)} className="rounded bg-danger-soft/50 p-2 text-left font-mono text-xs text-danger">
            <span className="block">
              {t('transform.at', { line: x.line, column: x.column ?? 1 })}
              {x.message.replace(/^expression: /, '')}
            </span>
            <span aria-hidden className="block whitespace-pre text-foreground">
              {lines[x.line - 1] ?? ''}
            </span>
            <span aria-hidden data-testid="expr-caret" className="block whitespace-pre">
              {' '.repeat(Math.max((x.column ?? 1) - 1, 0))}^
            </span>
          </button>
        ) : (
          <p key={`${x.code}:${x.message}`} role="alert" className="font-mono text-xs text-danger">
            {x.message}
          </p>
        ),
      )}
    </div>
  )
}
