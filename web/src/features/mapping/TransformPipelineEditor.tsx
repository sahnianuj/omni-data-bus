import { ArrowDown, ArrowUp, Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { ExpressionEditor } from './ExpressionEditor'
import { opCategories, validateArgs, type OpName } from './opSchemas'
import type { MappingIssue, TransformStep } from './types'

type ArgKind = 'text' | 'number' | 'bool' | 'value' | 'lookup' | 'select' | 'expr'
type ArgDef = { name: string; kind: ArgKind; options?: string[] }

/** The arguments each op's form shows. */
export const opArgs: Record<OpName, ArgDef[]> = {
  toString: [],
  parseInteger: [{ name: 'strict', kind: 'bool' }],
  parseNumber: [{ name: 'strict', kind: 'bool' }],
  parseBoolean: [{ name: 'strict', kind: 'bool' }],
  toDatetime: [{ name: 'inputFormat', kind: 'text' }, { name: 'timezone', kind: 'text' }, { name: 'strict', kind: 'bool' }],
  formatDatetime: [{ name: 'format', kind: 'select', options: ['iso8601', 'date', 'epoch_ms', 'epoch_s', 'custom'] }, { name: 'pattern', kind: 'text' }, { name: 'timezone', kind: 'text' }],
  now: [{ name: 'at', kind: 'select', options: ['run', 'write'] }, { name: 'format', kind: 'select', options: ['iso8601', 'date', 'epoch_ms', 'epoch_s', 'custom'] }, { name: 'pattern', kind: 'text' }, { name: 'timezone', kind: 'text' }],
  trim: [],
  lower: [],
  upper: [],
  substring: [{ name: 'start', kind: 'number' }, { name: 'length', kind: 'number' }],
  replace: [{ name: 'pattern', kind: 'text' }, { name: 'replacement', kind: 'text' }, { name: 'regex', kind: 'bool' }],
  concat: [{ name: 'separator', kind: 'text' }, { name: 'skipNulls', kind: 'bool' }],
  split: [{ name: 'separator', kind: 'text' }, { name: 'trim', kind: 'bool' }],
  default: [{ name: 'value', kind: 'value' }],
  nullIf: [{ name: 'value', kind: 'value' }],
  constant: [{ name: 'value', kind: 'value' }],
  lookup: [{ name: 'map', kind: 'lookup' }, { name: 'fallback', kind: 'select', options: ['keep', 'null', 'value'] }, { name: 'value', kind: 'value' }],
  round: [{ name: 'digits', kind: 'number' }],
  jsonParse: [],
  mask: [{ name: 'keepLast', kind: 'number' }, { name: 'char', kind: 'text' }],
  hash: [{ name: 'algo', kind: 'select', options: ['sha256', 'sha1'] }],
  expression: [{ name: 'expr', kind: 'expr' }],
  array: [{ name: 'dropNulls', kind: 'bool' }],
}

/** Text typed for a value arg: JSON literals (numbers, true, null, "…") become typed values. */
export function parseLiteral(text: string): unknown {
  const s = text.trim()
  if (s === '') return ''
  if (/^-?\d+(\.\d+)?$/.test(s) || s === 'true' || s === 'false' || s === 'null' || s.startsWith('"')) {
    try {
      return JSON.parse(s)
    } catch {
      return text
    }
  }
  return text
}

const showLiteral = (v: unknown) => (v === undefined ? '' : typeof v === 'string' ? v : JSON.stringify(v))

function parseLookup(text: string): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const line of text.split('\n')) {
    const i = line.indexOf('=')
    if (i > 0) out[line.slice(0, i).trim()] = parseLiteral(line.slice(i + 1))
  }
  return out
}

const showLookup = (m: unknown) =>
  m && typeof m === 'object' ? Object.entries(m as Record<string, unknown>).map(([k, v]) => `${k}=${showLiteral(v)}`).join('\n') : ''

type Props = {
  steps: TransformStep[]
  onChange: (steps: TransformStep[]) => void
  /** Server compile issues for this mapping (expression positions, bad regex, …). */
  issues?: MappingIssue[]
  /** One-click conversions to offer (e.g. formatDatetime epoch_ms for datetime → integer). */
  suggestions?: TransformStep[]
  readOnly?: boolean
}

/** Edits a field mapping's ordered transform steps (OC-020 AC8). */
export function TransformPipelineEditor({ steps, onChange, issues = [], suggestions = [], readOnly }: Props) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [filter, setFilter] = useState('')
  const setStep = (i: number, s: TransformStep) => onChange(steps.map((x, j) => (j === i ? s : x)))
  const setArg = (i: number, name: string, value: unknown) => {
    const args = { ...(steps[i]!.args ?? {}) }
    if (value === undefined || value === '') delete args[name]
    else args[name] = value
    setStep(i, { ...steps[i]!, args: Object.keys(args).length ? args : undefined })
  }
  const moveStep = (i: number, d: -1 | 1) => {
    const next = [...steps]
    const [s] = next.splice(i, 1)
    next.splice(i + d, 0, s!)
    onChange(next)
  }
  const add = (op: string) => {
    onChange([...steps, { op }])
    setOpen(false)
    setFilter('')
  }

  return (
    <div className="flex flex-col gap-2">
      {suggestions.length && !readOnly ? (
        <div className="flex flex-wrap gap-1.5">
          {suggestions.map((s) => (
            <button
              key={s.op + JSON.stringify(s.args)}
              type="button"
              onClick={() => onChange([s, ...steps])}
              className="rounded-full border border-primary/40 bg-primary-soft px-2.5 py-0.5 text-xs text-primary-soft-foreground hover:bg-primary-soft/70"
            >
              {t('transform.suggest', { op: t(`transform.op.${s.op}`) })}
            </button>
          ))}
        </div>
      ) : null}
      <ol className="flex flex-col gap-2" aria-label={t('transform.steps')}>
        {steps.map((s, i) => {
          const errors = validateArgs(s.op, s.args)
          const server = issues.filter((x) => x.opIndex === i)
          return (
            <li key={i} className="rounded-lg border border-border bg-surface p-3" aria-label={t('transform.stepLabel', { n: i + 1, op: t(`transform.op.${s.op}`) })}>
              <div className="flex items-center gap-2">
                <span className="flex size-5 items-center justify-center rounded-full bg-surface-muted text-[11px] text-muted">{i + 1}</span>
                <span className="text-[13px] font-medium">{t(`transform.op.${s.op}`)}</span>
                {!readOnly ? (
                  <span className="ml-auto flex gap-0.5">
                    <Button variant="ghost" size="icon" className="size-7" disabled={i === 0} aria-label={t('transform.moveUp')} onClick={() => moveStep(i, -1)}>
                      <ArrowUp aria-hidden />
                    </Button>
                    <Button variant="ghost" size="icon" className="size-7" disabled={i === steps.length - 1} aria-label={t('transform.moveDown')} onClick={() => moveStep(i, 1)}>
                      <ArrowDown aria-hidden />
                    </Button>
                    <Button variant="ghost" size="icon" className="size-7" aria-label={t('transform.remove')} onClick={() => onChange(steps.filter((_, j) => j !== i))}>
                      <Trash2 aria-hidden />
                    </Button>
                  </span>
                ) : null}
              </div>
              <div className="mt-2 grid grid-cols-1 gap-2 sm:grid-cols-2">
                {(opArgs[s.op as OpName] ?? []).map((a) => {
                  const id = `step-${i}-${a.name}`
                  const v = s.args?.[a.name]
                  const label = t(`transform.arg.${a.name}`)
                  if (a.kind === 'expr') {
                    // Not a <label> wrapper: the diagnostics below the editor are buttons.
                    return (
                      <div key={a.name} className="flex flex-col gap-1 sm:col-span-2">
                        <label htmlFor={id} className="text-xs text-muted">
                          {label}
                        </label>
                        <ExpressionEditor id={id} value={(v as string) ?? ''} disabled={readOnly} issues={server} onChange={(x) => setArg(i, a.name, x)} />
                      </div>
                    )
                  }
                  return (
                    <label key={a.name} htmlFor={id} className={a.kind === 'lookup' ? 'flex flex-col gap-1 sm:col-span-2' : 'flex flex-col gap-1'}>
                      <span className="text-xs text-muted">{label}</span>
                      {a.kind === 'bool' ? (
                        <input id={id} type="checkbox" checked={v === true} disabled={readOnly} onChange={(e) => setArg(i, a.name, e.target.checked || undefined)} />
                      ) : a.kind === 'select' ? (
                        <select id={id} value={(v as string) ?? ''} disabled={readOnly} onChange={(e) => setArg(i, a.name, e.target.value || undefined)} className="h-8 rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 text-xs">
                          <option value="" />
                          {a.options!.map((o) => (
                            <option key={o} value={o}>
                              {o}
                            </option>
                          ))}
                        </select>
                      ) : a.kind === 'lookup' ? (
                        <textarea
                          id={id}
                          rows={4}
                          spellCheck={false}
                          disabled={readOnly}
                          value={showLookup(v)}
                          onChange={(e) => setArg(i, a.name, parseLookup(e.target.value))}
                          placeholder={t('transform.lookupHint')}
                          className="rounded-[var(--radius-control)] border border-border-strong bg-surface p-2 font-mono text-xs"
                        />
                      ) : (
                        <Input
                          id={id}
                          disabled={readOnly}
                          className="h-8 font-mono text-xs"
                          type={a.kind === 'number' ? 'number' : 'text'}
                          value={a.kind === 'value' ? showLiteral(v) : ((v as string | number | undefined) ?? '')}
                          onChange={(e) => setArg(i, a.name, a.kind === 'number' ? (e.target.value === '' ? undefined : Number(e.target.value)) : a.kind === 'value' ? parseLiteral(e.target.value) : e.target.value)}
                          invalid={!!errors[a.name]}
                        />
                      )}
                      {errors[a.name] ? <span className="text-xs text-danger">{errors[a.name]}</span> : null}
                    </label>
                  )
                })}
              </div>
              {(s.op === 'expression' ? [] : server).map((x) => (
                <p key={x.code + x.message} role="alert" className="mt-2 font-mono text-xs text-danger">
                  {x.line ? t('transform.at', { line: x.line, column: x.column }) : ''}
                  {x.message}
                </p>
              ))}
            </li>
          )
        })}
      </ol>
      {!readOnly ? (
        <Popover open={open} onOpenChange={setOpen}>
          <PopoverTrigger asChild>
            <Button variant="secondary" size="sm" className="self-start">
              <Plus aria-hidden />
              {t('transform.add')}
            </Button>
          </PopoverTrigger>
          <PopoverContent align="start" className="w-72 p-2">
            <Input autoFocus value={filter} onChange={(e) => setFilter(e.target.value)} placeholder={t('transform.searchOps')} aria-label={t('transform.searchOps')} className="mb-2 h-8 text-xs" />
            <div className="max-h-72 overflow-y-auto">
              {opCategories.map((c) => {
                const opsHere = c.ops.filter((o) => t(`transform.op.${o}`).toLowerCase().includes(filter.toLowerCase()) || o.toLowerCase().includes(filter.toLowerCase()))
                if (!opsHere.length) return null
                return (
                  <div key={c.id} className="mb-1.5">
                    <p className="px-2 py-1 text-[11px] font-medium uppercase tracking-wide text-subtle">{t(`transform.cat.${c.id}`)}</p>
                    {opsHere.map((o) => (
                      <button key={o} type="button" onClick={() => add(o)} className="block w-full rounded px-2 py-1 text-left text-[13px] hover:bg-surface-muted">
                        {t(`transform.op.${o}`)}
                      </button>
                    ))}
                  </div>
                )
              })}
            </div>
          </PopoverContent>
        </Popover>
      ) : null}
    </div>
  )
}
