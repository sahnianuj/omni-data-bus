import { useId, useRef, useState, type KeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { insertToken, tokenAtCaret } from './analyzeKey'
import type { SourceColumn } from './types'

type Props = {
  value: string
  onChange: (value: string) => void
  columns: SourceColumn[]
  invalid?: boolean
  'aria-describedby'?: string
}

/** Key template input: typing `{` opens a column list; choosing one inserts `{col}` (OC-016 AC6). */
export function KeyTemplateInput({ value, onChange, columns, invalid, ...rest }: Props) {
  const { t } = useTranslation()
  const ref = useRef<HTMLInputElement>(null)
  const listId = useId()
  const [token, setToken] = useState<{ start: number; query: string; caret: number } | null>(null)
  const [active, setActive] = useState(0)

  const options = token ? columns.filter((c) => c.name.toLowerCase().includes(token.query.toLowerCase())).slice(0, 8) : []
  const open = options.length > 0

  const sync = (v: string, caret: number) => {
    const tk = tokenAtCaret(v, caret)
    setToken(tk ? { ...tk, caret } : null)
    setActive(0)
  }

  const choose = (name: string) => {
    if (!token) return
    const next = insertToken(value, token.caret, token.start, name)
    onChange(next.value)
    setToken(null)
    requestAnimationFrame(() => ref.current?.setSelectionRange(next.caret, next.caret))
  }

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (!open) return
    if (e.key === 'ArrowDown') setActive((a) => (a + 1) % options.length)
    else if (e.key === 'ArrowUp') setActive((a) => (a - 1 + options.length) % options.length)
    else if (e.key === 'Enter' || e.key === 'Tab') choose(options[active]!.name)
    else if (e.key === 'Escape') setToken(null)
    else return
    e.preventDefault()
  }

  return (
    <div className="relative">
      <input
        ref={ref}
        role="combobox"
        aria-label={t('key.template')}
        aria-expanded={open}
        aria-controls={listId}
        aria-autocomplete="list"
        aria-activedescendant={open ? `${listId}-${active}` : undefined}
        aria-invalid={invalid || undefined}
        aria-describedby={rest['aria-describedby']}
        spellCheck={false}
        value={value}
        onChange={(e) => {
          onChange(e.target.value)
          sync(e.target.value, e.target.selectionStart ?? e.target.value.length)
        }}
        onKeyDown={onKeyDown}
        onBlur={() => setTimeout(() => setToken(null), 100)}
        className={cn(
          'h-9 w-full rounded-[var(--radius-control)] border border-border-strong bg-surface px-3 font-mono text-[13px]',
          invalid && 'border-danger',
        )}
      />
      {open ? (
        <ul id={listId} role="listbox" aria-label={t('key.columns')} className="absolute z-20 mt-1 w-full rounded-md border border-border bg-surface p-1 shadow-lg">
          {options.map((c, i) => (
            <li
              key={c.name}
              id={`${listId}-${i}`}
              role="option"
              aria-selected={i === active}
              onMouseDown={(e) => {
                e.preventDefault()
                choose(c.name)
              }}
              className={cn('flex cursor-default items-center gap-2 rounded px-2 py-1 text-[13px]', i === active && 'bg-surface-muted')}
            >
              <span className="font-mono">{c.name}</span>
              <span className="text-xs text-subtle">{c.dataType}</span>
              {c.isPrimaryKey ? <span className="ml-auto text-[11px] text-muted">PK</span> : c.isUnique ? <span className="ml-auto text-[11px] text-muted">UQ</span> : null}
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  )
}
