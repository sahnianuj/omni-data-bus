import { Link2, Sparkles, Unlink } from 'lucide-react'
import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Can } from '@/features/auth/Can'
import { Input } from '@/components/ui/input'
import { useSchema, useSourceTable } from '@/features/discovery/api'
import { runAutomapScope } from '@/features/mapping/api'
import { srcKey } from '@/features/mapping/embeds'
import { ComputedEditor } from '@/features/computed/ComputedEditor'
import { NowSourceEditor } from '@/features/computed/NowSourceEditor'
import { useValueMode, ValueSourceControl } from '@/features/computed/ValueSourceControl'
import { activeFor, connect, OpError, replaceSource, setConstant, setNotPopulated, setTransforms, unmap, valueSourceOf } from '@/features/mapping/mappingOps'
import { useWorkbench, useWorkbenchStore } from '@/features/mapping/store'
import { parseLiteral, TransformPipelineEditor } from '@/features/mapping/TransformPipelineEditor'
import type { CanvasColumn, DocField, FieldType, MappingSpec, SourceColumn } from '@/features/mapping/types'
import { ApiError } from '@/lib/api/client'
import { nodeOf, renameNode, setExpression, setNodeType, setOnNoMatch, setVariantRule, unbind } from './composerOps'
import { expressionHint, useExpressionCheck } from './expressionCheck'
import { boundRelations, expressionNames, scope, type ScopeEntry } from './scope'

const selectCls = 'h-8 w-full rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 text-[13px]'
const TYPES: FieldType[] = ['string', 'integer', 'number', 'boolean', 'datetime', 'object', 'array', 'any']

/** An in-scope column: the value of the Source picker is "relationId|column|from". */
export type ScopeColumn = { entry: ScopeEntry; column: SourceColumn; from?: 'via' }
const valueOf = (c: { relation?: string; column: string; from?: string }) => `${c.relation ?? ''}|${c.column}|${c.from ?? ''}`

/** The columns a field may read, grouped by in-scope table (OC-047 scope rule, OC-048 AC4). */
export function useScopeColumns(spec: MappingSpec, fieldId: string): { entry: ScopeEntry; columns: ScopeColumn[] }[] {
  const view = useWorkbench((s) => s.view)
  const rootColumns = useWorkbench((s) => s.columns)
  const snap = useSchema(`${view.projectId}::source`)
  return useMemo(() => {
    const tableCols = (schema: string, name: string): SourceColumn[] =>
      snap.data?.schemas.find((s) => s.name === schema)?.tables.find((tb) => tb.name === name)?.columns ?? []
    // Inside rows of the parent's own table (the group's rows), each row before the parent's,
    // whose columns are its first row only (OC-065).
    const entries = scope(spec, fieldId) ?? []
    const own = spec.relations.find((r) => r.id === entries.at(-1)?.relationId)
    const parentTable = own ? (own.parentId ? spec.relations.find((r) => r.id === own.parentId)?.table : spec.root?.table) : undefined
    const sameTable = !!own && (own.group || (!!parentTable && parentTable.schema === own.table.schema && parentTable.name === own.table.name))
    return (sameTable ? [entries.at(-1)!, ...entries.slice(0, -1)] : entries).map((entry) => {
      const cols = entry.relationId === '' ? rootColumns : (view.embedTables?.[entry.relationId]?.columns ?? tableCols(entry.table.schema, entry.table.name))
      const out: ScopeColumn[] = cols.map((column) => ({ entry, column }))
      const r = spec.relations.find((x) => x.id === entry.relationId)
      if (r?.via) {
        const via = tableCols(r.via.table.schema, r.via.table.name)
        for (const name of r.via.columns ?? []) {
          const column = via.find((c) => c.name === name)
          if (column) out.push({ entry, column, from: 'via' })
        }
      }
      return { entry, columns: out }
    })
  }, [spec, fieldId, rootColumns, view.embedTables, snap.data])
}

type Props = {
  /** Increments when "Map from column…" asks for the source picker. */
  focusSource: number
  onBindTable: (nodeId: string, lookup?: boolean) => void
  /** The table the field's key refers to, and embedding it (OC-064). */
  embedTable?: string
  /** Why the embed action is disabled (the depth limit), if it is. */
  embedBlocked?: string
  onEmbed?: (fieldId: string) => void
}

/**
 * The Inspector (OC-048): for an object or array, its binding (table, kind, join, order, limit,
 * filter, shape, overflow, adapter) with Auto-map for its scope (OC-049); for a field, its source
 * — in-scope columns only, grouped by table — or a constant or expression with in-scope names,
 * and its transforms (OC-020).
 */
export function ComposerInspector({ focusSource, onBindTable, embedTable, embedBlocked, onEmbed }: Props) {
  const { t } = useTranslation()
  const store = useWorkbenchStore()
  const spec = useWorkbench((s) => s.history.present)
  const selection = useWorkbench((s) => s.selection)
  const apply = useWorkbench((s) => s.apply)
  const replace = useWorkbench((s) => s.replace)
  const view = useWorkbench((s) => s.view)
  const issues = useWorkbench((s) => s.issues)
  const nodeId = selection?.kind === 'field' ? selection.id : spec.docSchema.root.id
  const node = nodeOf(spec, nodeId)
  const isRoot = nodeId === spec.docSchema.root.id
  const rel = boundRelations(spec).get(nodeId)
  const groups = useScopeColumns(spec, nodeId)
  // Inside rows of the parent's own table (the group's rows), the parent's columns are its
  // first row: say so, so they aren't picked by mistake (OC-065).
  const firstRowOf = useMemo(() => {
    const own = spec.relations.find((r) => r.id === groups[0]?.entry.relationId)
    if (!own) return undefined
    const parent = own.parentId ?? ''
    const parentTable = parent ? spec.relations.find((r) => r.id === parent)?.table : spec.root?.table
    return own.group || (parentTable && parentTable.schema === own.table.schema && parentTable.name === own.table.name) ? parent : undefined
  }, [spec, groups])
  const mapping = activeFor(spec, nodeId)
  const expr = mapping?.transforms.find((s) => s.op === 'expression')
  const sourceRef = useRef<HTMLSelectElement>(null)
  const [constant, setConstantText] = useState('')
  const valueMode = useValueMode(nodeId)
  const fixedValue = mapping?.transforms.find((s) => s.op === 'constant')?.args?.value
  // The expression being typed, per node; otherwise the node's saved expression.
  const [draft, setDraft] = useState<{ node: string; text: string } | null>(null)
  const saved = typeof expr?.args?.expr === 'string' ? expr.args.expr : ''
  const exprText = draft?.node === nodeId ? draft.text : saved
  const setExprText = (fn: string | ((x: string) => string)) => setDraft({ node: nodeId, text: typeof fn === 'string' ? fn : fn(exprText) })
  // The text as typed is checked by the server's compiler before it can be used.
  const exprCheck = useExpressionCheck(view.projectId, view.id, spec, nodeId, exprText, exprText.trim() !== saved)
  const [busy, setBusy] = useState(false)
  const nameId = useId()
  const typeId = useId()
  const sourceId = useId()
  const exprId = useId()

  useEffect(() => {
    if (focusSource > 0) sourceRef.current?.focus()
  }, [focusSource])

  if (!node) return null
  const run = (fn: (s: MappingSpec) => MappingSpec) => {
    try {
      apply(fn)
    } catch (e) {
      if (e instanceof OpError) toast.error(e.message)
      else throw e
    }
  }
  const isContainer = node.type === 'object' || node.type === 'array'
  const current = mapping && mapping.sources.length === 1 && !expr ? valueOf(mapping.sources[0]!) : ''
  const names = expressionNames(spec, nodeId)
  const mappingIssues = issues.filter((i) => i.fieldMappingId && i.fieldMappingId === mapping?.id)

  const pick = (value: string) => {
    if (!value) return run((s) => unmap(s, nodeId))
    const [relation, column, from] = value.split('|') as [string, string, string]
    const col = groups.flatMap((g) => g.columns).find((c) => c.entry.relationId === relation && c.column.name === column && (c.from ?? '') === from)
    if (!col) return
    const canvas: CanvasColumn = relation
      ? { ...col.column, name: srcKey({ column, relation, from: from || undefined }), embed: relation, column, ...(from ? { from: 'via' as const } : {}) }
      : col.column
    run((s) => {
      const existing = activeFor(s, nodeId)
      if (existing) return replaceSource(s, existing.id, canvas)
      const res = connect(s, canvas, nodeId)
      return res.outcome === 'connected' ? res.spec : s
    })
  }

  const autoMap = async () => {
    setBusy(true)
    try {
      await store.getState().flush()
      const s = store.getState()
      replace(await runAutomapScope(view.projectId, s.view.id, s.version, { nodeId }))
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const path = (() => {
    const find = (f: typeof spec.docSchema.root, prefix: string): string | undefined => {
      for (const c of f.children ?? []) {
        const p = prefix ? `${prefix}.${c.name}` : c.name
        if (c.id === nodeId) return p
        const r = find(c, p) ?? (c.items ? (c.items.id === nodeId ? `${p}[]` : find(c.items, `${p}[]`)) : undefined)
        if (r) return r
      }
      return undefined
    }
    return isRoot ? '$' : (find(spec.docSchema.root, '') ?? node.name)
  })()

  return (
    <section aria-label={t('composer.inspector')} className="flex flex-col gap-4 overflow-y-auto p-3 text-[13px]">
      <header className="flex flex-col gap-0.5">
        <h2 className="text-xs font-semibold uppercase tracking-wide text-muted">{t('composer.inspector')}</h2>
        <span className="truncate font-mono text-[13px]">{path}</span>
      </header>
      {spec.fieldMappings.some((m) => m.targetFieldId === nodeId && m.state === 'notPopulated') ? (
        // Left empty on purpose, as the design says (OC-166): counted as done, nothing written.
        <p role="note" className="flex flex-wrap items-center gap-2 rounded bg-surface-muted p-2 text-xs text-muted">
          {t('propose.notPopulatedNote')}
          <Can role="editor">
            <Button size="sm" variant="ghost" className="h-6 px-2 text-xs" onClick={() => run((s) => setNotPopulated(s, nodeId, false))}>
              {t('propose.mapInstead')}
            </Button>
          </Can>
        </p>
      ) : null}

      {!isRoot && node.name !== '[]' && !rel && !mapping && !spec.fieldMappings.some((m) => m.targetFieldId === nodeId && m.state === 'notPopulated') && !(spec.aggregates ?? []).some((a) => a.targetFieldId === nodeId) ? (
        // Nothing fills it: map it, or leave it empty on purpose so it counts as done (OC-178).
        <Can role="editor">
          <Button size="sm" variant="secondary" className="h-7 self-start text-xs" onClick={() => run((s) => setNotPopulated(s, nodeId, true))}>
            {t('propose.markNotPopulated')}
          </Button>
        </Can>
      ) : null}

      {!isRoot && node.name !== '[]' ? (
        <div className="grid grid-cols-2 gap-2">
          <label htmlFor={nameId} className="col-span-2 flex flex-col gap-1 text-xs font-medium text-muted">
            {t('composer.name')}
            <Input
              id={nameId}
              key={`${nodeId}:${node.name}`}
              defaultValue={node.name}
              className="h-8"
              onBlur={(e) => e.target.value.trim() && e.target.value.trim() !== node.name && run((s) => renameNode(s, nodeId, e.target.value.trim()))}
              onKeyDown={(e) => e.key === 'Enter' && (e.target as HTMLInputElement).blur()}
            />
          </label>
          <label htmlFor={typeId} className="flex flex-col gap-1 text-xs font-medium text-muted">
            {t('composer.type')}
            <select id={typeId} className={selectCls} value={node.type} onChange={(e) => run((s) => setNodeType(s, nodeId, e.target.value as FieldType))}>
              {TYPES.map((x) => (
                <option key={x}>{x}</option>
              ))}
            </select>
          </label>
          {node.example !== undefined ? (
            <dl className="col-span-2 flex flex-col gap-1 text-xs">
              <dt className="font-medium text-muted">{t('composer.example')}</dt>
              <dd className="break-all font-mono">{JSON.stringify(node.example)}</dd>
            </dl>
          ) : null}
        </div>
      ) : null}

      {isContainer && !isRoot ? (
        <div className="flex flex-col gap-2 rounded-[var(--radius-card)] border border-border p-3">
          {rel ? (
            <>
              <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-[13px]">
                <dt className="text-muted">{t('composer.boundLabel')}</dt>
                <dd className="flex items-center gap-1 font-medium">
                  <Link2 className="size-3.5 text-primary" aria-hidden />
                  {rel.table.name}
                </dd>
                <dt className="text-muted">{t('composer.kind')}</dt>
                <dd>
                  {rel.kind}
                  {node.binding?.adapter ? ` · ${t(`composer.adapter.${node.binding.adapter}`)}` : ''}
                </dd>
                <dt className="text-muted">{t('composer.join')}</dt>
                <dd className="font-mono text-xs">
                  {rel.via
                    ? t('composer.viaJoin', { via: rel.via.table.name })
                    : (rel.join ?? []).map((j) => `${j.parent} = ${j.child}`).join(', ')}
                </dd>
                {rel.kind !== 'many-to-one' ? (
                  <>
                    <dt className="text-muted">{t('embed.orderBy')}</dt>
                    <dd>{rel.orderBy?.length ? rel.orderBy.map((o) => `${o.column} ${o.desc ? '↓' : '↑'}`).join(', ') : t('embed.orderNone')}</dd>
                    <dt className="text-muted">{t('embed.limit')}</dt>
                    <dd>{rel.limit ?? 1000}</dd>
                    <dt className="text-muted">{t('embed.filter')}</dt>
                    <dd>{rel.filter?.length ? rel.filter.map((f) => `${f.column} ${f.op}`).join(', ') : '—'}</dd>
                    <dt className="text-muted">{t('embed.shape')}</dt>
                    <dd>{t(`embed.shape${(rel.shape ?? 'objects')[0]!.toUpperCase()}${(rel.shape ?? 'objects').slice(1)}`)}</dd>
                    <dt className="text-muted">{t('embed.overflow')}</dt>
                    <dd>{t(`embed.overflow${(rel.overflow ?? 'truncate')[0]!.toUpperCase()}${(rel.overflow ?? 'truncate').slice(1)}`)}</dd>
                  </>
                ) : null}
              </dl>
              {node.type === 'array' && node.items?.children?.some((c) => c.variant) ? <VariantRules spec={spec} relationId={rel.id} table={rel.table} items={node.items} onChange={run} /> : null}
              <div className="flex flex-wrap gap-2">
                <Button size="sm" variant="secondary" onClick={() => onBindTable(nodeId)}>
                  {t('composer.rebind')}
                </Button>
                <Button size="sm" variant="ghost" onClick={() => run((s) => unbind(s, nodeId))}>
                  <Unlink aria-hidden />
                  {t('composer.unbind')}
                </Button>
                <Button size="sm" variant="ghost" loading={busy} onClick={() => void autoMap()}>
                  <Sparkles aria-hidden />
                  {t('composer.autoMapNode')}
                </Button>
              </div>
            </>
          ) : (
            <>
              <p className="text-muted">{t(node.type === 'array' ? 'composer.unboundArray' : 'composer.unboundObject')}</p>
              <Button size="sm" variant="secondary" className="self-start" onClick={() => onBindTable(nodeId)}>
                <Link2 aria-hidden />
                {t('composer.bindTable')}
              </Button>
            </>
          )}
        </div>
      ) : null}

      {isRoot ? (
        <div className="flex flex-col gap-2">
          <p className="text-muted">{spec.root?.groupBy?.length ? t('composer.rootHintGrouped', { columns: spec.root.groupBy.join(', ') }) : t('composer.rootHint')}</p>
          <Button size="sm" variant="secondary" className="self-start" loading={busy} onClick={() => void autoMap()}>
            <Sparkles aria-hidden />
            {t('composer.autoMapNode')}
          </Button>
        </div>
      ) : null}

      {!isContainer ? (
        <div className="flex flex-col gap-2">
          <ValueSourceControl fieldId={nodeId} fixed={valueMode.fixed} onFixedChange={valueMode.onFixedChange} />
          {valueSourceOf(spec, nodeId) === 'template' ? <ComputedEditor key={nodeId} fieldId={nodeId} /> : null}
          {valueSourceOf(spec, nodeId) === 'now' ? <NowSourceEditor key={nodeId} fieldId={nodeId} /> : null}
        </div>
      ) : null}
      {(!isContainer && valueMode.mode === 'fixed') || (node.type === 'array' && !rel) ? (
        // Fixed: the same value in every document (an unbound array can always take one).
        <div className="flex items-end gap-2">
          <label className="flex flex-1 flex-col gap-1 text-xs font-medium text-muted">
            {t('composer.constant')}
            <Input value={constant} placeholder={fixedValue !== undefined ? JSON.stringify(fixedValue) : undefined} onChange={(e) => setConstantText(e.target.value)} className="h-8 font-mono text-xs" />
          </label>
          <Button size="sm" variant="secondary" disabled={!constant} onClick={() => run((s) => setConstant(s, nodeId, parseLiteral(constant)))}>
            {t('composer.setConstant')}
          </Button>
        </div>
      ) : null}
      {(!isContainer || (node.type === 'array' && !rel)) && (isContainer || valueMode.mode === 'source') ? (
        <div className="flex flex-col gap-3">
          <label htmlFor={sourceId} className="flex flex-col gap-1 text-xs font-medium text-muted">
            {t('composer.source')}
            <select ref={sourceRef} id={sourceId} aria-label={t('composer.sourceFor', { name: node.name })} className={selectCls} value={current} onChange={(e) => pick(e.target.value)}>
              <option value="">{mapping && !current ? t('composer.computed') : t('composer.noSource')}</option>
              {groups.map((g) => (
                <optgroup key={g.entry.relationId || 'root'} label={firstRowOf === g.entry.relationId ? t('composer.firstRowGroup', { table: g.entry.table.name }) : g.entry.relationId === '' ? t('composer.rootGroup', { table: g.entry.table.name }) : g.entry.lookup ? t('composer.lookupGroup', { table: g.entry.table.name }) : spec.relations.some((r) => r.id === g.entry.relationId && r.group) ? t('composer.groupGroup', { table: g.entry.table.name }) : g.entry.table.name}>
                  {g.columns.map((c) => (
                    <option key={valueOf({ relation: c.entry.relationId, column: c.column.name, from: c.from })} value={valueOf({ relation: c.entry.relationId, column: c.column.name, from: c.from })}>
                      {c.from ? `${c.column.name} (${t('composer.junction')})` : c.column.name} · {c.column.jsonType}
                    </option>
                  ))}
                </optgroup>
              ))}
            </select>
          </label>
          <div className="flex flex-col gap-1">
            <label htmlFor={exprId} className="text-xs font-medium text-muted">
              {t('composer.expression')}
            </label>
            <textarea
              id={exprId}
              rows={2}
              spellCheck={false}
              value={exprText}
              onChange={(e) => setExprText(e.target.value)}
              placeholder={t('composer.exprPlaceholder')}
              className="rounded-[var(--radius-control)] border border-border-strong bg-surface p-2 font-mono text-xs"
            />
            <div className="flex flex-wrap items-center gap-1" aria-label={t('composer.exprNames')} role="group">
              {names.map((n) => (
                <button key={n} type="button" onClick={() => setExprText((x) => `${x}${n}`)} className="rounded border border-border px-1.5 py-0.5 font-mono text-[11px] hover:bg-surface-muted">
                  {n}
                </button>
              ))}
              <Button size="sm" variant="secondary" className="ml-auto" disabled={!exprText.trim() || exprCheck.state === 'error'} onClick={() => run((s) => setExpression(s, nodeId, exprText.trim()))}>
                {t('composer.setExpression')}
              </Button>
            </div>
            {exprCheck.state === 'error' ? (
              <p role="alert" className="text-xs text-danger">
                {exprCheck.message}
              </p>
            ) : exprCheck.state === 'checking' ? (
              <p className="text-xs text-subtle">{t('composer.exprChecking')}</p>
            ) : exprCheck.state === 'ok' ? (
              <p className="truncate font-mono text-xs text-muted" aria-label={t('composer.exprSample')}>
                → {exprCheck.value === undefined ? '—' : JSON.stringify(exprCheck.value)}
              </p>
            ) : null}
          </div>
          {mapping && !expr && mapping.sources.length > 0 ? (
            <TransformPipelineEditor steps={mapping.transforms} onChange={(steps) => run((s) => setTransforms(s, mapping.id, steps))} issues={mappingIssues} />
          ) : null}
          {mappingIssues.map((i) => (
            <p key={`${i.code}:${i.message}`} role="alert" className="text-xs text-danger">
              {i.op === 'expression' ? expressionHint(i.message) : i.message}
            </p>
          ))}
          {mapping ? (
            <div className="flex flex-wrap gap-2">
              <Button size="sm" variant="ghost" onClick={() => run((s) => unmap(s, nodeId))}>
                {t('composer.unmap')}
              </Button>
              {embedTable && onEmbed ? (
                <Button size="sm" variant="secondary" disabled={!!embedBlocked} title={embedBlocked} onClick={() => onEmbed(nodeId)}>
                  {t('composer.embedHere', { table: embedTable })}
                </Button>
              ) : null}
            </div>
          ) : null}
        </div>
      ) : null}
    </section>
  )
}

/**
 * Which rows of the array's relation are each variant of its items (OC-171): a column of the
 * rows that is set, or equal to a value; and what a row of none does.
 */
function VariantRules({ spec, relationId, table, items, onChange }: { spec: MappingSpec; relationId: string; table: { schema: string; name: string }; items: DocField; onChange: (fn: (s: MappingSpec) => MappingSpec) => void }) {
  const { t } = useTranslation()
  const view = useWorkbench((s) => s.view)
  const cols = useSourceTable(`${view.projectId}::source`, table.schema, table.name)
  const rel = spec.relations.find((r) => r.id === relationId)
  const id = useId()
  const columns = cols.data?.columns ?? []
  return (
    <fieldset className="flex flex-col gap-1.5 rounded border border-border p-2" aria-label={t('composer.variants.title')}>
      <legend className="px-1 text-xs font-semibold uppercase tracking-wide text-muted">{t('composer.variants.title')}</legend>
      <p className="text-xs text-muted">{t('composer.variants.hint', { table: table.name })}</p>
      {(items.children ?? [])
        .filter((v) => v.variant)
        .map((v) => {
          const rule = rel?.variants?.find((x) => x.fieldId === v.id)
          return (
            <div key={v.id} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)] items-center gap-1.5 text-xs">
              <span className="truncate font-medium">{v.name}</span>
              <select
                aria-label={`${v.name}: ${t('composer.variants.when')}`}
                className="h-7 rounded border border-border-strong bg-surface px-1"
                value={rule?.when ?? ''}
                onChange={(e) => onChange((s) => setVariantRule(s, relationId, v.id, e.target.value ? { when: e.target.value, value: rule?.value } : null))}
              >
                <option value="">{t('composer.variants.none')}</option>
                {columns.map((c) => (
                  <option key={c.name} value={c.name}>
                    {c.name}
                  </option>
                ))}
              </select>
              <input
                aria-label={`${v.name}: ${t('composer.variants.value')}`}
                placeholder={t('composer.variants.value')}
                className="h-7 rounded border border-border-strong bg-surface px-1"
                defaultValue={rule?.value ?? ''}
                key={`${v.id}:${rule?.value ?? ''}`}
                disabled={!rule?.when}
                onBlur={(e) => rule?.when && e.target.value !== (rule.value ?? '') && onChange((s) => setVariantRule(s, relationId, v.id, { when: rule.when, value: e.target.value }))}
              />
            </div>
          )
        })}
      <label htmlFor={id} className="flex items-center gap-1.5 text-xs text-muted">
        {t('composer.variants.noMatch')}
        <select id={id} className="h-7 rounded border border-border-strong bg-surface px-1" value={rel?.onNoMatch ?? 'skip'} onChange={(e) => onChange((s) => setOnNoMatch(s, relationId, e.target.value as 'skip' | 'fail'))}>
          <option value="skip">{t('composer.variants.skip')}</option>
          <option value="fail">{t('composer.variants.fail')}</option>
        </select>
      </label>
    </fieldset>
  )
}
