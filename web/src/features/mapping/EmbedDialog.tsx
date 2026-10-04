import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useSourceTable } from '@/features/discovery/api'
import { rename } from '@/lib/naming'
import { addEmbed, type AddEmbedRequest } from './api'
import { FilterChips, type FilterColumn } from './FilterChips'
import { useWorkbench, useWorkbenchStore } from './store'
import type { JunctionCandidate, RelationFilter, SourceColumn } from './types'
import { HelpLink } from '@/components/HelpLink'

const selectCls = 'h-8 w-full rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 text-[13px]'

/** Default array limit (mapping.DefaultEmbedLimit). */
export const DEFAULT_EMBED_LIMIT = 1000

type TableRef = { schema: string; name: string }
type JoinPair = { parent: string; child: string }

/** One hop of a relation path: its direction, table and join (from the Related tree, OC-042). */
export type EmbedHop = { kind: 'many-to-one' | 'one-to-many'; table: TableRef; join: JoinPair[]; virtual?: boolean }

/**
 * What the Embed dialog adds: a path of hops (one hop for a direct relation; several nest
 * each under the one before, OC-043 AC7), or a many-to-many junction candidate (OC-044) —
 * under the root, or under an embedded relation (parentId).
 */
export type EmbedPlan =
  | { mode: 'hops'; hops: EmbedHop[]; parentId?: string; parentLabel?: string }
  | { mode: 'junction'; junction: JunctionCandidate; parentId?: string; parentLabel?: string }

type Shape = 'objects' | 'scalars' | 'keyed'

/**
 * The Embed dialog (OC-021 AC2, extended by OC-043 – OC-046). The shape follows the relation:
 * many-to-one is a nested object or values only (lookup); one-to-many and many-to-many are an
 * array of objects, values or a keyed object, ordered, limited, filtered, with an overflow
 * policy, a read strategy and optional aggregates. Fields can be generated and auto-mapped.
 */
export function EmbedDialog({ plan, onClose }: { plan: EmbedPlan; onClose: () => void }) {
  const { t } = useTranslation()
  const store = useWorkbenchStore()
  const view = useWorkbench((s) => s.view)
  const replace = useWorkbench((s) => s.replace)
  const conn = `${view.projectId}::source`
  const junction = plan.mode === 'junction' ? plan.junction : undefined
  const hops: EmbedHop[] = plan.mode === 'hops' ? plan.hops : []
  const last = junction ? { kind: 'many-to-many' as const, table: junction.table } : hops[hops.length - 1]!
  const many = last.kind !== 'many-to-one'
  const table = useSourceTable(conn, last.table.schema, last.table.name)
  const via = useSourceTable(conn, junction?.via.schema ?? '', junction?.via.name ?? '')
  const [names, setNames] = useState<string[]>(() => (junction ? [rename(junction.table.name)] : hops.map((h) => rename(h.table.name))))
  const [lookup, setLookup] = useState(false)
  const [shape, setShape] = useState<Shape>('objects')
  const [valueColumn, setValueColumn] = useState('')
  const [keyColumn, setKeyColumn] = useState('')
  const [orderBy, setOrderBy] = useState('')
  const [desc, setDesc] = useState(false)
  const [limit, setLimit] = useState(DEFAULT_EMBED_LIMIT)
  const [empty, setEmpty] = useState<'array' | 'omit'>('array')
  const [overflow, setOverflow] = useState<'truncate' | 'fail' | 'split'>('truncate')
  const [strategy, setStrategy] = useState<'auto' | 'batched' | 'sorted-merge'>('auto')
  const [filter, setFilter] = useState<RelationFilter[]>([])
  const [payload, setPayload] = useState<string[]>(() => junction?.payloadColumns ?? [])
  const [distinct, setDistinct] = useState(false)
  const [keepDangling, setKeepDangling] = useState(false)
  const [count, setCount] = useState(false)
  const [sumColumn, setSumColumn] = useState('')
  const [generate, setGenerate] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const limitId = useId()

  const cols: SourceColumn[] = table.data?.columns ?? []
  // A many-to-many relation can also order and filter on the junction's columns.
  const viaCols: FilterColumn[] = junction ? (via.data?.columns ?? []).map((c) => ({ ...c, from: 'via' as const })) : []
  const orderable: FilterColumn[] = [...cols, ...viaCols]
  const numeric = cols.filter((c) => c.jsonType === 'integer' || c.jsonType === 'number')
  const name = names[names.length - 1] ?? ''
  const valueType = cols.find((c) => c.name === valueColumn)?.jsonType ?? '?'

  /** What the dialog creates, e.g. "items: array<object>" (the design changes on confirm). */
  const creates = (() => {
    if (!many) return lookup ? t('embed.createsLookup', { table: last.table.name }) : `${name}: object`
    if (shape === 'scalars') return `${name}: array<${valueType}>`
    if (shape === 'keyed') return `${name}: object (${t('embed.keyedBy', { column: keyColumn || '?' })})`
    return `${name}: array<object>`
  })()

  const submit = async () => {
    setBusy(true)
    setError(null)
    try {
      await store.getState().flush()
      const opts: Partial<AddEmbedRequest> = {}
      if (many) {
        opts.limit = limit
        opts.empty = empty
        if (orderBy) {
          const fromVia = orderBy.startsWith('via:')
          opts.orderBy = [{ column: orderBy.replace(/^via:/, ''), desc, ...(fromVia ? { from: 'via' as const } : {}) }]
        }
        if (shape !== 'objects') opts.shape = shape
        if (shape === 'scalars') opts.valueColumn = valueColumn
        if (shape === 'keyed') opts.keyColumn = keyColumn
        if (overflow !== 'truncate') opts.overflow = overflow
        if (strategy !== 'auto') opts.strategy = strategy
        if (filter.length > 0) opts.filter = filter
        const aggregates: NonNullable<AddEmbedRequest['aggregates']> = []
        if (count) aggregates.push({ fn: 'count', targetName: rename(`${name}_count`) })
        if (sumColumn) aggregates.push({ fn: 'sum', column: sumColumn, targetName: rename(`${name}_${sumColumn}_total`) })
        if (aggregates.length > 0) opts.aggregates = aggregates
      }
      let body: AddEmbedRequest
      if (junction) {
        body = {
          kind: 'many-to-many', table: junction.table, targetName: name.trim(), generate,
          via: { table: junction.via, parentJoin: junction.parentJoin, childJoin: junction.childJoin, columns: payload },
          ...opts,
          ...(distinct ? { distinct } : {}),
          ...(keepDangling ? { keepDangling } : {}),
        }
      } else {
        const reqs: AddEmbedRequest[] = hops.map((h, i) => ({ kind: h.kind, table: h.table, join: h.join, targetName: names[i]!.trim(), generate }))
        const tail = reqs[reqs.length - 1]!
        Object.assign(tail, opts)
        if (!many && lookup) tail.lookupOnly = true
        body = reqs[0]!
        if (reqs.length > 1) body.then = reqs.slice(1)
      }
      if (plan.parentId) body.parentId = plan.parentId
      replace(await addEmbed(view.projectId, view.id, store.getState().version, body))
      onClose()
    } catch (e) {
      setError(e)
    } finally {
      setBusy(false)
    }
  }

  const invalid =
    names.some((n) => n.trim() === '' && !(lookup && !many)) ||
    (many && (limit < 1 || limit > 100000)) ||
    (many && shape === 'scalars' && !valueColumn) ||
    (many && shape === 'keyed' && !keyColumn)
  const title = junction ? t('embed.titleJunction', { table: junction.table.name, via: junction.via.name }) : t('embed.title', { table: last.table.name })

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <div className="flex items-center gap-1.5">
            <DialogTitle>{title}</DialogTitle>
            <HelpLink topic="embed" />
          </div>
          <DialogDescription>
            {t('embed.description', { table: `${last.table.schema}.${last.table.name}` })}
            {plan.parentLabel ? ` ${t('embed.under', { parent: plan.parentLabel })}` : ''}
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-3 text-[13px]">
          {hops.length > 1 ? (
            <div className="rounded-[var(--radius-control)] bg-surface-muted px-3 py-2 text-xs" data-testid="embed-path">
              {t('embed.path')}: {hops.map((h) => `${h.kind === 'one-to-many' ? '↙' : '↗'} ${h.table.name}`).join(' → ')}
            </div>
          ) : null}
          <div className="flex items-center justify-between">
            <span className="text-muted">{t('embed.kind')}</span>
            <span>{t(junction ? 'embed.kindManyMany' : many ? 'embed.kindMany' : 'embed.kindOne')}</span>
          </div>
          {!many ? (
            <label className="grid gap-1">
              <span className="text-muted">{t('embed.mode')}</span>
              <select className={selectCls} value={lookup ? 'lookup' : 'object'} onChange={(e) => setLookup(e.target.value === 'lookup')}>
                <option value="object">{t('embed.modeObject')}</option>
                <option value="lookup">{t('embed.modeLookup')}</option>
              </select>
            </label>
          ) : null}
          {hops.length > 1
            ? hops.map((h, i) => (
                <label key={`${h.table.name}-${i}`} className="grid gap-1">
                  <span className="text-muted">{t('embed.fieldNameFor', { table: h.table.name })}</span>
                  <Input value={names[i]} onChange={(e) => setNames(names.map((n, j) => (j === i ? e.target.value : n)))} />
                </label>
              ))
            : !(lookup && !many) ? (
                <label className="grid gap-1">
                  <span className="text-muted">{t('embed.fieldName')}</span>
                  <Input value={name} onChange={(e) => setNames([e.target.value])} />
                </label>
              ) : null}
          {many ? (
            <>
              <label className="grid gap-1">
                <span className="text-muted">{t('embed.shape')}</span>
                <select className={selectCls} value={shape} onChange={(e) => setShape(e.target.value as Shape)}>
                  <option value="objects">{t('embed.shapeObjects')}</option>
                  <option value="scalars">{t('embed.shapeScalars')}</option>
                  <option value="keyed">{t('embed.shapeKeyed')}</option>
                </select>
              </label>
              {shape === 'scalars' ? (
                <label className="grid gap-1">
                  <span className="text-muted">{t('embed.valueColumn')}</span>
                  <select className={selectCls} value={valueColumn} onChange={(e) => setValueColumn(e.target.value)}>
                    <option value="">{t('embed.pickColumn')}</option>
                    {cols.map((c) => (
                      <option key={c.name} value={c.name}>
                        {c.name}
                      </option>
                    ))}
                  </select>
                </label>
              ) : null}
              {shape === 'keyed' ? (
                <label className="grid gap-1">
                  <span className="text-muted">{t('embed.keyColumn')}</span>
                  <select className={selectCls} value={keyColumn} onChange={(e) => setKeyColumn(e.target.value)}>
                    <option value="">{t('embed.pickColumn')}</option>
                    {cols.map((c) => (
                      <option key={c.name} value={c.name}>
                        {c.name}
                      </option>
                    ))}
                  </select>
                </label>
              ) : null}
              {junction ? (
                <fieldset className="grid gap-1">
                  <legend className="mb-1 text-muted">{t('embed.payload', { via: junction.via.name })}</legend>
                  {junction.payloadColumns.length === 0 ? <span className="text-xs text-subtle">{t('embed.payloadNone')}</span> : null}
                  {junction.payloadColumns.map((c) => (
                    <label key={c} className="flex items-center gap-2">
                      <input type="checkbox" checked={payload.includes(c)} onChange={(e) => setPayload(e.target.checked ? [...payload, c] : payload.filter((x) => x !== c))} />
                      <span className="font-mono text-xs">{c}</span>
                    </label>
                  ))}
                </fieldset>
              ) : null}
              <div className="grid grid-cols-[1fr_auto] items-end gap-2">
                <label className="grid gap-1">
                  <span className="text-muted">{t('embed.orderBy')}</span>
                  <select className={selectCls} value={orderBy} onChange={(e) => setOrderBy(e.target.value)}>
                    <option value="">{t('embed.orderNone')}</option>
                    {orderable.map((c) => (
                      <option key={`${c.from ?? ''}${c.name}`} value={c.from === 'via' ? `via:${c.name}` : c.name}>
                        {c.from === 'via' ? `${junction?.via.name}.${c.name}` : c.name}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="flex h-8 items-center gap-1.5">
                  <input type="checkbox" checked={desc} disabled={!orderBy} onChange={(e) => setDesc(e.target.checked)} />
                  {t('embed.desc')}
                </label>
              </div>
              <div className="grid gap-1">
                <label htmlFor={limitId} className="text-muted">
                  {t('embed.limit')}
                </label>
                <span className="flex items-center gap-2">
                  <Input id={limitId} type="number" min={1} max={100000} className="w-28" aria-describedby={`${limitId}-hint`} value={limit} onChange={(e) => setLimit(Number(e.target.value))} />
                  <span id={`${limitId}-hint`} className="text-subtle">
                    {t('embed.limitHint')}
                  </span>
                </span>
              </div>
              <label className="grid gap-1">
                <span className="text-muted">{t('embed.overflow')}</span>
                <select className={selectCls} value={overflow} onChange={(e) => setOverflow(e.target.value as typeof overflow)}>
                  <option value="truncate">{t('embed.overflowTruncate')}</option>
                  <option value="fail">{t('embed.overflowFail')}</option>
                  <option value="split">{t('embed.overflowSplit')}</option>
                </select>
              </label>
              <label className="grid gap-1">
                <span className="text-muted">{t('embed.empty')}</span>
                <select className={selectCls} value={empty} onChange={(e) => setEmpty(e.target.value as 'array' | 'omit')}>
                  <option value="array">{t('embed.emptyArray')}</option>
                  <option value="omit">{t('embed.emptyOmit')}</option>
                </select>
              </label>
              <div className="grid gap-1">
                <span className="text-muted">{t('embed.filter')}</span>
                <FilterChips columns={orderable} value={filter} onChange={setFilter} />
              </div>
              <fieldset className="grid gap-1">
                <legend className="mb-1 text-muted">{t('embed.aggregates')}</legend>
                <label className="flex items-center gap-2" title={t('embed.aggregatesHint')}>
                  <input type="checkbox" checked={count} onChange={(e) => setCount(e.target.checked)} />
                  {t('embed.aggCount', { name: rename(`${name}_count`) })}
                </label>
                <label className="flex items-center gap-2">
                  <span className="shrink-0">{t('embed.aggSum')}</span>
                  <select aria-label={t('embed.aggSum')} className={selectCls} value={sumColumn} onChange={(e) => setSumColumn(e.target.value)}>
                    <option value="">{t('embed.aggNone')}</option>
                    {numeric.map((c) => (
                      <option key={c.name} value={c.name}>
                        {c.name}
                      </option>
                    ))}
                  </select>
                </label>
              </fieldset>
              <label className="grid gap-1">
                <span className="text-muted">{t('embed.strategy')}</span>
                <select className={selectCls} value={strategy} onChange={(e) => setStrategy(e.target.value as typeof strategy)}>
                  <option value="auto">{t('embed.strategyAuto')}</option>
                  <option value="batched">{t('embed.strategyBatched')}</option>
                  <option value="sorted-merge">{t('embed.strategyMerge')}</option>
                </select>
              </label>
              {junction ? (
                <>
                  <label className="flex items-start gap-2">
                    <input type="checkbox" className="mt-0.5" checked={distinct} onChange={(e) => setDistinct(e.target.checked)} />
                    <span>
                      {t('embed.distinct')}
                      <span className="block text-xs text-subtle">{t('embed.distinctHint')}</span>
                    </span>
                  </label>
                  <label className="flex items-start gap-2">
                    <input type="checkbox" className="mt-0.5" checked={keepDangling} onChange={(e) => setKeepDangling(e.target.checked)} />
                    <span>
                      {t('embed.keepDangling')}
                      <span className="block text-xs text-subtle">{t('embed.keepDanglingHint')}</span>
                    </span>
                  </label>
                </>
              ) : null}
            </>
          ) : null}
          {!(many && shape === 'scalars') ? (
            <label className="flex items-start gap-2">
              <input type="checkbox" className="mt-0.5" checked={generate} onChange={(e) => setGenerate(e.target.checked)} />
              <span>
                {t('embed.generate')}
                <span className="block text-xs text-subtle">
                  {t(junction ? 'embed.generateHintJunction' : lookup ? 'embed.generateHintLookup' : last.kind === 'many-to-one' ? 'embed.generateHintObject' : 'embed.generateHint')}
                </span>
              </span>
            </label>
          ) : null}
          <div className="rounded-[var(--radius-control)] border border-dashed border-border px-3 py-2 font-mono text-xs" data-testid="embed-creates">
            {t('embed.creates')}: {creates}
          </div>
        </div>
        {error ? <ErrorState error={error} /> : null}
        <DialogFooter>
          <Button variant="secondary" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button loading={busy} disabled={invalid} onClick={() => void submit()}>
            {t('embed.submit')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
