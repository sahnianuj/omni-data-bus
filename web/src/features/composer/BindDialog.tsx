import { useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ErrorState } from '@/components/ErrorState'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useSourceTable } from '@/features/discovery/api'
import { runAutomapScope } from '@/features/mapping/api'
import { DEFAULT_EMBED_LIMIT } from '@/features/mapping/EmbedDialog'
import { FilterChips } from '@/features/mapping/FilterChips'
import { useRelationGraph, useTablesHops } from '@/features/mapping/relationsApi'
import { useWorkbench, useWorkbenchStore } from '@/features/mapping/store'
import type { RelationFilter } from '@/features/mapping/types'
import { VirtualRelationshipDialog } from '@/features/mapping/VirtualRelationshipDialog'
import { kindOf, prefill, scopeTable, type BindOption, type JoinPair } from './bindPrefill'
import { addLookup, bind, containerOf, lookupScope, nodeOf, OpError, repeatsTable, type BindInput, type TableRef } from './composerOps'

const selectCls = 'h-8 w-full rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 text-[13px]'
const joinText = (join: JoinPair[]) => join.map((j) => `${j.parent} = ${j.child}`).join(', ')

type Props = {
  nodeId: string
  table: TableRef
  /** "Values only": a lookup-only relation whose columns can fill any field in the node's scope. */
  lookup?: boolean
  onClose: () => void
  /** Called instead of onClose when the dialog is dismissed without binding. */
  onCancel?: () => void
}

/**
 * The Bind dialog (OC-048): binds an object or array node to a table dropped on it (or picked
 * with "Bind to table…"), pre-filled from the relationship graph between the node's scope and
 * that table — kind and join, many-to-many through a junction with its payload columns — with
 * the array options (order, limit, filter, shape, overflow), generated item fields and a scoped
 * auto-map (OC-049). Without a path it offers to define a relationship (OC-042).
 */
export function BindDialog({ nodeId, table, lookup, onClose, onCancel }: Props) {
  const { t } = useTranslation()
  const store = useWorkbenchStore()
  const view = useWorkbench((s) => s.view)
  const spec = useWorkbench((s) => s.history.present)
  const apply = useWorkbench((s) => s.apply)
  const replace = useWorkbench((s) => s.replace)
  const own = useMemo(() => ({ schema: view.sourceSchema, name: view.sourceTable }), [view.sourceSchema, view.sourceTable])
  const driving = spec.root?.table ?? own
  const conn = `${view.projectId}::source`
  const graph = useRelationGraph(conn, `${driving.schema}.${driving.name}`)
  // A lookup joins from the relation it hangs under (addLookup): a bound node's own table.
  const lookupRel = lookup ? spec.relations.find((r) => r.id === lookupScope(spec, nodeId)) : undefined
  const scopeT = lookupRel?.table ?? scopeTable(spec, nodeId, own)
  const scopeHops = useTablesHops(conn, [`${scopeT.schema}.${scopeT.name}`])
  const hops = scopeHops.hopsOf(scopeT)
  const junctions = scopeHops.junctionsOf(scopeT)
  const pf = useMemo(() => prefill(graph.data, spec, nodeId, table, own, hops ? { hops, junctions } : undefined, lookupRel?.table), [graph.data, spec, nodeId, table, own, hops, junctions, lookupRel])
  const options = useMemo(() => (lookup ? pf.options.filter((o) => o.type === 'direct' && o.kind === 'many-to-one') : pf.options), [pf.options, lookup])
  const node = nodeOf(spec, nodeId)
  const isArray = node?.type === 'array'
  const cols = useSourceTable(conn, table.schema, table.name)
  const fromCols = useSourceTable(conn, pf.from.schema, pf.from.name)
  const [choice, setChoice] = useState(0)
  const [joins, setJoins] = useState<Record<number, JoinPair[]>>({})
  const [payload, setPayload] = useState<Record<number, string[]>>({})
  const [orderBy, setOrderBy] = useState('')
  const [desc, setDesc] = useState(false)
  const [limit, setLimit] = useState(DEFAULT_EMBED_LIMIT)
  const [filter, setFilter] = useState<RelationFilter[]>([])
  const [shape, setShape] = useState<'objects' | 'scalars' | 'keyed'>('objects')
  const [valueColumn, setValueColumn] = useState('')
  const [keyColumn, setKeyColumn] = useState('')
  const [overflow, setOverflow] = useState<'truncate' | 'fail' | 'split'>('truncate')
  const [generate, setGenerate] = useState(() => (containerOf(spec, nodeId)?.children?.length ?? 0) === 0)
  const [autoMap, setAutoMap] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [defining, setDefining] = useState(false)
  const limitId = useId()
  const groupId = useId()

  const opt: BindOption | undefined = options[Math.min(choice, options.length - 1)]
  const kind = opt ? kindOf(opt) : undefined
  const many = kind !== undefined && kind !== 'many-to-one'
  const join = opt?.type === 'direct' ? (joins[choice] ?? opt.join) : []
  const tableCols = cols.data?.columns ?? []
  const repeat = lookup && opt?.type === 'direct' ? repeatsTable(spec, nodeId, table, join) : null

  const label = (o: BindOption) => {
    if (o.type === 'm2m') return t('composer.bind.m2m', { via: o.via.name })
    return t(o.kind === 'many-to-one' ? 'composer.bind.m2o' : 'composer.bind.o2m', { join: joinText(o.join) })
  }

  const submit = async () => {
    if (!opt) return
    setBusy(true)
    setError(null)
    try {
      if (lookup && opt.type === 'direct') {
        apply((s) => addLookup(s, nodeId, { table, join }).spec)
      } else {
        const input: BindInput =
          opt.type === 'm2m'
            ? { kind: 'many-to-many', table, via: { table: opt.via, parentJoin: opt.parentJoin, childJoin: opt.childJoin, columns: payload[choice] ?? opt.payloadColumns } }
            : { kind: opt.kind, table, join }
        if (many) {
          input.limit = limit
          if (orderBy) input.orderBy = [{ column: orderBy, desc: desc || undefined }]
          if (filter.length > 0) input.filter = filter
          if (overflow !== 'truncate') input.overflow = overflow
          if (isArray && shape !== 'objects') {
            input.shape = shape
            if (shape === 'scalars') input.valueColumn = valueColumn
            if (shape === 'keyed') input.keyColumn = keyColumn
          }
        }
        if (generate && shape === 'objects') {
          const joinCols = new Set(kind === 'one-to-many' ? join.map((j) => j.child) : [])
          input.generate = tableCols.filter((c) => !joinCols.has(c.name))
        }
        apply((s) => bind(s, nodeId, input).spec)
        if (autoMap && shape === 'objects') {
          await store.getState().flush()
          const s = store.getState()
          replace(await runAutomapScope(view.projectId, s.view.id, s.version, { nodeId }))
        }
      }
      onClose()
    } catch (e) {
      setError(e instanceof OpError ? new Error(e.message) : e)
    } finally {
      setBusy(false)
    }
  }

  if (defining) {
    return <VirtualRelationshipDialog initialFrom={`${pf.from.schema}.${pf.from.name}`} initialTo={`${table.schema}.${table.name}`} onClose={() => setDefining(false)} />
  }

  return (
    <Dialog open onOpenChange={(o) => !o && (onCancel ?? onClose)()}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{t(lookup ? 'composer.bind.lookupTitle' : 'composer.bind.title', { table: table.name, node: node?.name ?? '' })}</DialogTitle>
          <DialogDescription>{t('composer.bind.from', { from: `${pf.from.schema}.${pf.from.name}` })}</DialogDescription>
        </DialogHeader>
        {graph.isLoading ? (
          <p className="text-[13px] text-muted">{t('common.loading')}</p>
        ) : options.length === 0 ? (
          <div role="alert" className="flex flex-col items-start gap-3 rounded-[var(--radius-card)] border border-warning/40 bg-warning-soft p-3 text-[13px]">
            <span>{t('composer.bind.noPath', { from: pf.from.name, table: table.name })}</span>
            <Button size="sm" variant="secondary" onClick={() => setDefining(true)}>
              {t('composer.bind.define')}
            </Button>
          </div>
        ) : (
          <div className="flex max-h-[60vh] flex-col gap-4 overflow-y-auto pr-1">
            <fieldset className="flex flex-col gap-1.5">
              <legend id={groupId} className="mb-1 text-xs font-medium text-muted">
                {t('composer.bind.relation')}
              </legend>
              <div role="radiogroup" aria-labelledby={groupId} className="flex flex-col gap-1.5">
                {options.map((o, i) => (
                  <label key={i} className="flex items-start gap-2 rounded-[var(--radius-control)] border border-border p-2 text-[13px] has-[:checked]:border-primary">
                    <input type="radio" name="bind-option" checked={i === choice} onChange={() => setChoice(i)} className="mt-0.5" />
                    <span className="flex flex-1 flex-col gap-1">
                      <span className="flex flex-wrap items-center gap-1.5">
                        {label(o)}
                        {o.type === 'direct' && o.virtual ? <Badge tone="primary">{t('composer.bind.virtual')}</Badge> : null}
                      </span>
                      {o.type === 'm2m' && i === choice ? (
                        <span className="flex flex-wrap items-center gap-2 text-xs text-muted">
                          {t('composer.bind.payload')}
                          {o.payloadColumns.map((c) => {
                            const on = (payload[i] ?? o.payloadColumns).includes(c)
                            return (
                              <label key={c} className="flex items-center gap-1">
                                <input
                                  type="checkbox"
                                  checked={on}
                                  onChange={() => setPayload((p) => ({ ...p, [i]: on ? (p[i] ?? o.payloadColumns).filter((x) => x !== c) : [...(p[i] ?? o.payloadColumns), c] }))}
                                />
                                <span className="font-mono">{c}</span>
                              </label>
                            )
                          })}
                        </span>
                      ) : null}
                    </span>
                  </label>
                ))}
              </div>
            </fieldset>
            {!isArray && many ? <p className="text-xs text-muted">{t('composer.bind.firstExplain', { table: table.name })}</p> : null}
            {isArray && !many && !lookup ? <p className="text-xs text-muted">{t('composer.bind.wrapExplain')}</p> : null}

            {opt?.type === 'direct' ? (
              <div className="flex flex-col gap-1.5">
                <span className="text-xs font-medium text-muted">{t('composer.bind.join')}</span>
                {join.map((j, k) => (
                  <div key={k} className="flex items-center gap-2">
                    <select
                      aria-label={t('composer.bind.parentColumn', { n: k + 1 })}
                      className={selectCls}
                      value={j.parent}
                      onChange={(e) => setJoins((m) => ({ ...m, [choice]: join.map((x, n) => (n === k ? { ...x, parent: e.target.value } : x)) }))}
                    >
                      {[j.parent, ...(fromCols.data?.columns ?? []).map((c) => c.name).filter((c) => c !== j.parent)].map((c) => (
                        <option key={c}>{c}</option>
                      ))}
                    </select>
                    <span aria-hidden>=</span>
                    <select
                      aria-label={t('composer.bind.childColumn', { n: k + 1 })}
                      className={selectCls}
                      value={j.child}
                      onChange={(e) => setJoins((m) => ({ ...m, [choice]: join.map((x, n) => (n === k ? { ...x, child: e.target.value } : x)) }))}
                    >
                      {[j.child, ...tableCols.map((c) => c.name).filter((c) => c !== j.child)].map((c) => (
                        <option key={c}>{c}</option>
                      ))}
                    </select>
                  </div>
                ))}
              </div>
            ) : null}

            {many ? (
              <div className="grid grid-cols-2 gap-3">
                <label className="flex flex-col gap-1 text-xs font-medium text-muted">
                  {t('embed.orderBy')}
                  <select className={selectCls} value={orderBy} onChange={(e) => setOrderBy(e.target.value)}>
                    <option value="">{t('embed.orderNone')}</option>
                    {tableCols.map((c) => (
                      <option key={c.name}>{c.name}</option>
                    ))}
                  </select>
                </label>
                <label className="flex items-end gap-1.5 pb-1.5 text-xs text-muted">
                  <input type="checkbox" checked={desc} onChange={(e) => setDesc(e.target.checked)} />
                  {t('embed.desc')}
                </label>
                <label htmlFor={limitId} className="flex flex-col gap-1 text-xs font-medium text-muted">
                  {t('embed.limit')}
                  <Input id={limitId} type="number" min={1} max={100000} value={limit} onChange={(e) => setLimit(Number(e.target.value))} className="h-8" />
                </label>
                <label className="flex flex-col gap-1 text-xs font-medium text-muted">
                  {t('embed.overflow')}
                  <select className={selectCls} value={overflow} onChange={(e) => setOverflow(e.target.value as typeof overflow)}>
                    {(['truncate', 'fail', 'split'] as const).map((o) => (
                      <option key={o} value={o}>
                        {t(`embed.overflow${o[0]!.toUpperCase()}${o.slice(1)}`)}
                      </option>
                    ))}
                  </select>
                </label>
                {isArray ? (
                  <label className="col-span-2 flex flex-col gap-1 text-xs font-medium text-muted">
                    {t('embed.shape')}
                    <select className={selectCls} value={shape} onChange={(e) => setShape(e.target.value as typeof shape)}>
                      {(['objects', 'scalars', 'keyed'] as const).map((s) => (
                        <option key={s} value={s}>
                          {t(`embed.shape${s[0]!.toUpperCase()}${s.slice(1)}`)}
                        </option>
                      ))}
                    </select>
                  </label>
                ) : null}
                {isArray && shape !== 'objects' ? (
                  <label className="col-span-2 flex flex-col gap-1 text-xs font-medium text-muted">
                    {t(shape === 'scalars' ? 'embed.valueColumn' : 'embed.keyColumn')}
                    <select className={selectCls} value={shape === 'scalars' ? valueColumn : keyColumn} onChange={(e) => (shape === 'scalars' ? setValueColumn : setKeyColumn)(e.target.value)}>
                      <option value="" />
                      {tableCols.map((c) => (
                        <option key={c.name}>{c.name}</option>
                      ))}
                    </select>
                  </label>
                ) : null}
                <div className="col-span-2">
                  <FilterChips columns={tableCols} value={filter} onChange={setFilter} />
                </div>
              </div>
            ) : null}

            {!lookup && shape === 'objects' ? (
              <div className="flex flex-wrap gap-4 text-[13px]">
                <label className="flex items-center gap-1.5">
                  <input type="checkbox" checked={generate} onChange={(e) => setGenerate(e.target.checked)} />
                  {t('composer.bind.generate')}
                </label>
                <label className="flex items-center gap-1.5">
                  <input type="checkbox" checked={autoMap} onChange={(e) => setAutoMap(e.target.checked)} />
                  {t('composer.bind.autoMap')}
                </label>
              </div>
            ) : null}
            {error ? <ErrorState error={error} /> : null}
          </div>
        )}
        {repeat ? (
          // OC-177: a lookup that repeats a table the node reads already is never used.
          <p role="alert" className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-foreground">
            {t(repeat === 'bound' ? 'composer.bind.repeatBound' : 'composer.bind.repeatLookup', { node: node?.name ?? '', table: table.name })}
          </p>
        ) : null}
        <DialogFooter>
          <Button variant="secondary" onClick={onCancel ?? onClose}>
            {t('common.cancel')}
          </Button>
          <Button onClick={() => void submit()} loading={busy} disabled={!opt || (isArray && shape === 'scalars' && !valueColumn) || (isArray && shape === 'keyed' && !keyColumn)}>
            {t(lookup ? 'composer.bind.addLookup' : 'composer.bind.confirm')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
