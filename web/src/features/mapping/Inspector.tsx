import { useQuery } from '@tanstack/react-query'
import { EyeOff, Plus, X } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { findField } from '@/features/docschema/docschemaOps'
import { setRequired } from '@/features/docschema/docschemaOps'
import { previewMapping } from './api'
import { canConnect } from './canConnect'
import { KeyActions } from './KeyActions'
import { ComputedEditor } from '@/features/computed/ComputedEditor'
import { NowSourceEditor } from '@/features/computed/NowSourceEditor'
import { useValueMode, ValueSourceControl } from '@/features/computed/ValueSourceControl'
import { createFieldAndMap, ignoreColumn, setConstant, setNotPopulated, setTransforms, unignoreColumn, unmap, valueSourceOf } from './mappingOps'
import { useWorkbench } from './store'
import { parseLiteral, TransformPipelineEditor } from './TransformPipelineEditor'
import type { TransformStep } from './types'

function show(v: unknown): string {
  if (v === null || v === undefined) return 'null'
  return typeof v === 'string' ? JSON.stringify(v) : JSON.stringify(v)
}

/** Debounces a value (the mini-preview waits 300 ms after the last edit, OC-019 AC8). */
export function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setV(value), ms)
    return () => clearTimeout(id)
  }, [value, ms])
  return v
}

/** Before/after values of one field mapping for 3 sample rows. */
function MiniPreview({ projectId, mappingId, fieldMappingId }: { projectId: string; mappingId: string; fieldMappingId: string }) {
  const { t } = useTranslation()
  const spec = useWorkbench((s) => s.history.present)
  const debounced = useDebounced(spec, 300)
  const preview = useQuery({
    queryKey: ['mapping-field-preview', mappingId, fieldMappingId, debounced],
    queryFn: () => previewMapping(projectId, mappingId, { fieldMappingId, specOverride: debounced, limit: 3 }),
    retry: false,
    staleTime: 0,
  })
  return (
    <section aria-label={t('inspector.preview')} className="flex flex-col gap-1.5">
      <h4 className="text-xs font-medium uppercase tracking-wide text-subtle">{t('inspector.preview')}</h4>
      {preview.isPending ? <p className="text-xs text-muted">{t('common.loading')}</p> : null}
      {preview.error ? <p className="text-xs text-danger">{(preview.error as Error).message}</p> : null}
      <ul className="flex flex-col gap-1">
        {(preview.data?.fields ?? []).map((f, i) => (
          <li key={i} className="grid grid-cols-[1fr_auto_1fr] items-center gap-2 font-mono text-xs">
            <span className="truncate text-muted">{show(f.before)}</span>
            <span className="text-subtle">→</span>
            <span className={f.error ? 'truncate text-danger' : 'truncate'}>{f.error ? f.error.message : show(f.after)}</span>
          </li>
        ))}
      </ul>
    </section>
  )
}

/** The bottom sheet for the selected mapping, field or column (OC-019 AC8). */
export function Inspector({ projectId }: { projectId: string }) {
  const { t } = useTranslation()
  const selection = useWorkbench((s) => s.selection)
  const spec = useWorkbench((s) => s.history.present)
  const columns = useWorkbench((s) => s.columns)
  const issues = useWorkbench((s) => s.issues)
  const explain = useWorkbench((s) => s.view.explain)
  const mappingId = useWorkbench((s) => s.view.id)
  const apply = useWorkbench((s) => s.apply)
  const select = useWorkbench((s) => s.select)
  const [constant, setConstantText] = useState('')

  const mapping = useMemo(() => {
    if (selection?.kind === 'mapping') return spec.fieldMappings.find((m) => m.id === selection.id)
    if (selection?.kind === 'field') return spec.fieldMappings.find((m) => m.targetFieldId === selection.id && m.state !== 'rejected')
    return undefined
  }, [selection, spec.fieldMappings])
  const fieldId = mapping?.targetFieldId ?? (selection?.kind === 'field' ? selection.id : undefined)
  const field = fieldId ? findField(spec.docSchema, fieldId) : undefined
  const notPopulated = mapping?.state === 'notPopulated'
  // Source shows the mapped columns (or how to map one); Fixed, the value box.
  const valueMode = useValueMode(field?.field.id)
  const fixedValue = valueMode.mode === 'fixed' ? mapping?.transforms.find((s) => s.op === 'constant')?.args?.value : undefined
  if (!selection) return null

  const close = (
    <Button variant="ghost" size="icon" className="ml-auto size-7" aria-label={t('common.close')} onClick={() => select(null)}>
      <X aria-hidden />
    </Button>
  )

  if (selection.kind === 'column') {
    const col = columns.find((c) => c.name === selection.name)
    if (!col) return null
    const ignored = spec.ignoredColumns.includes(col.name)
    return (
      <aside aria-label={t('inspector.title')} className="flex flex-col gap-3 border-t border-border bg-surface px-6 py-4">
        <header className="flex items-center gap-2">
          <h3 className="font-mono text-sm font-semibold">{col.name}</h3>
          <span className="text-xs text-muted">{col.dataType}</span>
          {close}
        </header>
        <div className="flex gap-2">
          <Button size="sm" variant="secondary" onClick={() => apply((s) => (ignored ? unignoreColumn(s, col.name) : ignoreColumn(s, col.name)))}>
            <EyeOff aria-hidden />
            {ignored ? t('inspector.unignore') : t('inspector.ignore')}
          </Button>
          {!ignored ? (
            <Button size="sm" variant="secondary" onClick={() => apply((s) => createFieldAndMap(s, col, s.docSchema.root.id).spec)}>
              <Plus aria-hidden />
              {t('inspector.createField')}
            </Button>
          ) : null}
        </div>
      </aside>
    )
  }

  const src = mapping?.sources.length === 1 ? columns.find((c) => c.name === mapping.sources[0]!.column) : undefined
  const cc = src && field ? canConnect(src.jsonType, field.field.type) : undefined
  const suggestions: TransformStep[] = cc?.suggestedTransform && !(mapping?.transforms.length) ? [cc.suggestedTransform as TransformStep] : []
  const exp = mapping ? explain[mapping.id] : undefined

  return (
    <aside aria-label={t('inspector.title')} className="flex max-h-[45vh] flex-col gap-4 overflow-y-auto border-t border-border bg-surface px-6 py-4">
      <header className="flex items-center gap-2">
        <h3 className="text-sm font-semibold">
          {notPopulated
            ? t('inspector.notPopulated')
            : mapping?.transforms.some((s) => s.op === 'now') && !mapping.sources.length
              ? t('now.title')
              : mapping
                ? mapping.sources.map((s) => s.column).join(' + ') || t('inspector.constant')
                : t('inspector.unmapped')}
          <span className="px-1.5 text-subtle">→</span>
          <span className="font-mono">{field?.path}</span>
        </h3>
        {mapping && !notPopulated ? <span className="text-xs text-muted">{t(`mapping.state.${mapping.state === 'pending' ? 'suggested' : mapping.origin}`)}</span> : null}
        {close}
      </header>
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-[1fr_320px]">
        <div className="flex flex-col gap-3">
          {field && field.field.type !== 'object' && field.field.type !== 'array' ? <ValueSourceControl fieldId={field.field.id} fixed={valueMode.fixed} onFixedChange={valueMode.onFixedChange} /> : null}
          {notPopulated && field ? (
            // Left empty on purpose (OC-166): counted as done, nothing written.
            <p role="note" className="flex flex-wrap items-center gap-2 rounded bg-surface-muted p-2 text-xs text-muted">
              {t('propose.notPopulatedNote')}
              <Button size="sm" variant="ghost" className="h-6 px-2 text-xs" onClick={() => apply((s) => setNotPopulated(s, field.field.id, false))}>
                {t('propose.mapInstead')}
              </Button>
            </p>
          ) : field && valueSourceOf(spec, field.field.id) === 'now' ? (
            <NowSourceEditor key={field.field.id} fieldId={field.field.id} />
          ) : field && valueSourceOf(spec, field.field.id) === 'template' ? (
            <ComputedEditor key={field.field.id} fieldId={field.field.id} />
          ) : field && valueMode.mode === 'fixed' ? (
            <div className="flex flex-wrap items-end gap-2">
              <label className="flex flex-col gap-1 text-xs text-muted">
                {t('inspector.constantLabel')}
                <input
                  value={constant}
                  placeholder={fixedValue !== undefined ? JSON.stringify(fixedValue) : undefined}
                  onChange={(e) => setConstantText(e.target.value)}
                  className="h-8 rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 font-mono text-xs text-foreground"
                />
              </label>
              <Button size="sm" variant="secondary" disabled={!constant} onClick={() => apply((s) => setConstant(s, field.field.id, parseLiteral(constant)))}>
                {t('inspector.setConstant')}
              </Button>
            </div>
          ) : mapping ? (
            <>
              <h4 className="text-xs font-medium uppercase tracking-wide text-subtle">{t('inspector.transforms')}</h4>
              <TransformPipelineEditor
                steps={mapping.transforms}
                onChange={(steps) => apply((s) => setTransforms(s, mapping.id, steps))}
                issues={issues.filter((i) => i.fieldMappingId === mapping.id)}
                suggestions={suggestions}
              />
            </>
          ) : (
            <p className="text-[13px] text-muted">{t('inspector.dragHint')}</p>
          )}
          {field ? (
            <div className="flex flex-wrap items-end gap-2">
              {mapping ? (
                <Button size="sm" variant="destructive" onClick={() => apply((s) => unmap(s, field.field.id))}>
                  {t('mapping.unmap')}
                </Button>
              ) : null}
              <KeyActions fieldId={field.field.id} />
              {!mapping && !(spec.aggregates ?? []).some((a) => a.targetFieldId === field.field.id) ? (
                // Nothing fills it: leave it empty on purpose, so it counts as done (OC-178).
                <Button size="sm" variant="ghost" onClick={() => apply((s) => setNotPopulated(s, field.field.id, true))}>
                  {t('propose.markNotPopulated')}
                </Button>
              ) : null}
              {field.field.required && !mapping ? (
                <Button size="sm" variant="ghost" onClick={() => apply((s) => ({ ...s, docSchema: setRequired(s.docSchema, field.field.id, false) }))}>
                  {t('inspector.markOptional')}
                </Button>
              ) : null}
            </div>
          ) : null}
        </div>
        <div className="flex flex-col gap-4">
          {exp ? (
            <section aria-label={t('inspector.explain')} className="flex flex-col gap-1.5">
              <h4 className="text-xs font-medium uppercase tracking-wide text-subtle">{t('inspector.explain')}</h4>
              <div className="h-1.5 overflow-hidden rounded-full bg-surface-muted">
                <div className="h-full bg-[var(--map-auto)]" style={{ width: `${Math.round(exp.score * 100)}%` }} />
              </div>
              <p className="text-xs">{exp.reason}</p>
              <ul className="font-mono text-[11px] text-subtle">
                {exp.signals.map((s) => (
                  <li key={s.name}>
                    {s.name} {s.score > 0 ? '+' : ''}
                    {s.score.toFixed(2)}
                  </li>
                ))}
              </ul>
            </section>
          ) : null}
          {mapping && mapping.state === 'active' ? <MiniPreview projectId={projectId} mappingId={mappingId} fieldMappingId={mapping.id} /> : null}
        </div>
      </div>
    </aside>
  )
}
