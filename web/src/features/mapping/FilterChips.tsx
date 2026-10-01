import { Plus, X } from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import type { RelationFilter, SourceColumn } from './types'

const selectCls = 'h-8 rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 text-[13px]'

export const FILTER_OPS = ['eq', 'ne', 'lt', 'lte', 'gt', 'gte', 'in', 'notIn', 'isNull', 'notNull', 'like'] as const
type Op = (typeof FILTER_OPS)[number]
const noValue = new Set<Op>(['isNull', 'notNull'])
const listOps = new Set<Op>(['in', 'notIn'])

/** A filterable column: a table column, or a junction column (from 'via'). */
export type FilterColumn = SourceColumn & { from?: 'via' }

const colKey = (c: { name: string; from?: string }) => (c.from === 'via' ? `via:${c.name}` : c.name)

/** Converts typed text to the column's JSON type (the server checks it against the column type too). */
function typed(col: SourceColumn, raw: string): unknown {
  if (col.jsonType === 'integer' || col.jsonType === 'number') {
    const n = Number(raw)
    return raw.trim() === '' || Number.isNaN(n) ? raw : n
  }
  if (col.jsonType === 'boolean') return raw === 'true'
  return raw
}

function valueText(v: unknown): string {
  if (Array.isArray(v)) return v.map(String).join(', ')
  return v == null ? '' : String(v)
}

/**
 * Structured filter chips on a relation's rows (OC-045): column, operator and a value input
 * typed for the column (number, date, true/false or text; comma-separated for in / not in).
 * Conditions are ANDed and sent to the database as bind parameters.
 */
export function FilterChips({ columns, value, onChange }: { columns: FilterColumn[]; value: RelationFilter[]; onChange: (next: RelationFilter[]) => void }) {
  const { t } = useTranslation()
  const [col, setCol] = useState('')
  const [op, setOp] = useState<Op>('eq')
  const [raw, setRaw] = useState('')
  const id = useId()
  const column = columns.find((c) => colKey(c) === col)

  const add = () => {
    if (!column) return
    const f: RelationFilter = { column: column.name, op }
    if (column.from === 'via') f.from = 'via'
    if (listOps.has(op)) f.value = raw.split(',').map((s) => typed(column, s.trim())).filter((v) => v !== '')
    else if (!noValue.has(op)) f.value = typed(column, raw)
    onChange([...value, f])
    setRaw('')
  }

  const input = () => {
    if (!column || noValue.has(op)) return null
    const common = { id: `${id}-value`, 'aria-label': t('filter.value'), className: 'w-36' }
    if (listOps.has(op)) return <Input {...common} type="text" placeholder={t('filter.listHint')} value={raw} onChange={(e) => setRaw(e.target.value)} />
    switch (column.jsonType) {
      case 'integer':
      case 'number':
        return <Input {...common} type="number" step={column.jsonType === 'integer' ? 1 : 'any'} value={raw} onChange={(e) => setRaw(e.target.value)} />
      case 'datetime':
        return <Input {...common} type={column.udtName === 'date' ? 'date' : 'datetime-local'} value={raw} onChange={(e) => setRaw(e.target.value)} />
      case 'boolean':
        return (
          <select {...common} className={selectCls} value={raw} onChange={(e) => setRaw(e.target.value)}>
            <option value="">—</option>
            <option value="true">{t('common.yes')}</option>
            <option value="false">{t('common.no')}</option>
          </select>
        )
    }
    return <Input {...common} type="text" value={raw} onChange={(e) => setRaw(e.target.value)} />
  }

  return (
    <div className="grid gap-2" data-testid="filter-chips">
      {value.length > 0 ? (
        <ul className="flex flex-wrap gap-1.5">
          {value.map((f, i) => (
            <li key={`${f.column}-${i}`} className="inline-flex items-center gap-1 rounded-full border border-border bg-surface-muted py-0.5 pl-2.5 pr-1 text-xs">
              <span className="font-mono">
                {f.from === 'via' ? `${t('filter.via')} ` : ''}
                {f.column} {t(`filter.op.${f.op}`)} {noValue.has(f.op as Op) ? '' : valueText(f.value)}
              </span>
              <button
                type="button"
                className="rounded-full p-0.5 text-subtle hover:bg-surface hover:text-foreground"
                aria-label={t('filter.remove', { column: f.column })}
                onClick={() => onChange(value.filter((_, j) => j !== i))}
              >
                <X className="size-3" />
              </button>
            </li>
          ))}
        </ul>
      ) : null}
      <div className="flex flex-wrap items-center gap-1.5">
        <select aria-label={t('filter.column')} className={selectCls} value={col} onChange={(e) => setCol(e.target.value)}>
          <option value="">{t('filter.pickColumn')}</option>
          {columns.map((c) => (
            <option key={colKey(c)} value={colKey(c)}>
              {c.from === 'via' ? `${t('filter.via')} ${c.name}` : c.name}
            </option>
          ))}
        </select>
        <select aria-label={t('filter.operator')} className={selectCls} value={op} onChange={(e) => setOp(e.target.value as Op)}>
          {FILTER_OPS.filter((o) => o !== 'like' || column?.jsonType === 'string').map((o) => (
            <option key={o} value={o}>
              {t(`filter.op.${o}`)}
            </option>
          ))}
        </select>
        {input()}
        <Button size="sm" variant="secondary" disabled={!column || (!noValue.has(op) && raw.trim() === '')} onClick={add}>
          <Plus className="size-3.5" aria-hidden />
          {t('filter.add')}
        </Button>
      </div>
    </div>
  )
}
