import { ChevronDown, ChevronLeft, ChevronRight, ChevronUp, FileJson, Gauge } from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/button'
import { pathsById } from '@/features/docschema/docschemaOps'
import { previewMapping } from '@/features/mapping/api'
import { addToDesign, applyFix, removeBranch } from '@/features/mapping/fixes'
import { useWorkbench } from '@/features/mapping/store'
import type { MappingPreview, MappingSpec } from '@/features/mapping/types'
import { formatBytes } from '@/lib/format'
import { cn } from '@/lib/utils'
import { boundRelations } from './scope'

/** Sample root rows a preview reads (OC-049 reads at most 20 at once). */
export const SAMPLE_ROWS = 5
export const SAMPLE_DEBOUNCE_MS = 800

type Item = NonNullable<MappingPreview['items']>[number]

/** The array under the root the selected node belongs to (for the worst case), if any. */
function rootArrayOf(spec: MappingSpec, nodeId: string): string | undefined {
  const r = boundRelations(spec).get(nodeId)
  return r && !r.parentId && r.kind !== 'many-to-one' ? r.id : undefined
}

/**
 * The Sample pane (OC-048/049): the document the mapping builds for real sample rows, refreshed
 * 800 ms after an edit (unsaved edits included), with per-node counts ("showing 50 of 1,240")
 * and a worst case for the selected array. Clicking a value selects its field.
 */
export function SamplePane() {
  const { t } = useTranslation()
  const view = useWorkbench((s) => s.view)
  const spec = useWorkbench((s) => s.history.present)
  const selection = useWorkbench((s) => s.selection)
  const select = useWorkbench((s) => s.select)
  const [open, setOpen] = useState(true)
  const [index, setIndex] = useState(0)
  const [worst, setWorst] = useState<string | undefined>()
  const [result, setResult] = useState<MappingPreview | null>(null)
  const [error, setError] = useState<unknown>(null)
  const [loading, setLoading] = useState(false)
  // The customer's imported document beside the generated one (OC-059).
  const reference = spec.docSchema.reference
  const [showRef, setShowRef] = useState(true)
  const seq = useRef(0)
  const selected = selection?.kind === 'field' ? selection.id : spec.docSchema.root.id
  const worstFor = rootArrayOf(spec, selected)

  useEffect(() => {
    if (!open) return
    const n = ++seq.current
    const timer = setTimeout(() => {
      setLoading(true)
      previewMapping(view.projectId, view.id, { limit: SAMPLE_ROWS, specOverride: spec, ...(worst ? { worstCase: worst } : {}) })
        .then((r) => {
          if (n !== seq.current) return
          setResult(r)
          setError(null)
          setIndex((i) => Math.min(i, Math.max((r.items?.length ?? 1) - 1, 0)))
        })
        .catch((e: unknown) => n === seq.current && setError(e))
        .finally(() => n === seq.current && setLoading(false))
    }, SAMPLE_DEBOUNCE_MS)
    return () => clearTimeout(timer)
  }, [spec, view.projectId, view.id, worst, open])

  const idByPath = useMemo(() => new Map([...pathsById(spec.docSchema)].map(([id, p]) => [p, id])), [spec.docSchema])
  const items = result?.items ?? []
  const item: Item | undefined = items[index]
  const pick = (p: string) => {
    const id = idByPath.get(p)
    if (id) select({ kind: 'field', id })
  }
  const selectedPath = [...idByPath].find(([, id]) => id === selected)?.[0]

  return (
    // Its own light tint, so the sample stands out from the grey canvas (OC-077).
    <section aria-label={t('composer.sample.title')} className="flex max-h-[40vh] min-h-0 flex-col border-t border-border bg-sample-surface">
      <div className="flex flex-wrap items-center gap-2 px-3 py-1.5 text-[13px]">
        <button type="button" aria-expanded={open} onClick={() => setOpen((o) => !o)} className="flex items-center gap-1 font-medium">
          {open ? <ChevronDown className="size-3.5" aria-hidden /> : <ChevronUp className="size-3.5" aria-hidden />}
          {t('composer.sample.title')}
        </button>
        {open && items.length > 0 ? (
          <span className="flex items-center gap-1 text-muted">
            <button type="button" aria-label={t('composer.sample.prev')} disabled={index === 0} onClick={() => setIndex((i) => i - 1)} className="rounded p-0.5 hover:bg-surface-muted disabled:opacity-40">
              <ChevronLeft className="size-3.5" aria-hidden />
            </button>
            <span className="font-mono text-xs" aria-live="polite">
              {item?.key ?? '—'} ({index + 1}/{items.length})
            </span>
            <button type="button" aria-label={t('composer.sample.next')} disabled={index >= items.length - 1} onClick={() => setIndex((i) => i + 1)} className="rounded p-0.5 hover:bg-surface-muted disabled:opacity-40">
              <ChevronRight className="size-3.5" aria-hidden />
            </button>
          </span>
        ) : null}
        {open && item ? <span className="text-xs text-muted">{formatBytes(item.sizeBytes)}</span> : null}
        {open && item?.path ? <span className="truncate font-mono text-xs text-muted">{item.path}</span> : null}
        {open ? (
          <span className="ml-auto flex items-center gap-2">
            {loading ? <span className="text-xs text-muted">{t('composer.sample.refreshing')}</span> : null}
            {reference ? (
              <Button size="sm" variant="ghost" aria-pressed={showRef} title={t('composer.sample.referenceHint')} onClick={() => setShowRef((v) => !v)}>
                <FileJson aria-hidden />
                {t('composer.sample.reference')}
              </Button>
            ) : null}
            {worst ? (
              <Button size="sm" variant="ghost" onClick={() => setWorst(undefined)}>
                {t('composer.sample.backToSample')}
              </Button>
            ) : (
              <Button size="sm" variant="ghost" disabled={!worstFor} title={worstFor ? undefined : t('composer.sample.worstHint')} onClick={() => worstFor && setWorst(worstFor)}>
                <Gauge aria-hidden />
                {t('composer.sample.worstCase')}
              </Button>
            )}
          </span>
        ) : null}
      </div>
      {open ? (
        <div className="min-h-0 flex-1 overflow-auto px-3 pb-3">
          {error ? <ErrorState error={error} /> : null}
          {result?.worstCase ? (
            <p role="status" className="mb-2 text-xs text-muted">
              {t('composer.sample.worstResult', { rows: result.worstCase.rows.toLocaleString(), path: result.worstCase.path, size: formatBytes(result.worstCase.estimatedBytes) })}
            </p>
          ) : null}
          {item?.stats ? (
            <ul aria-label={t('composer.sample.stats')} className="mb-2 flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted">
              {Object.entries(item.stats).map(([path, s]) => (
                <li key={path}>
                  <span className="font-mono">{path}</span>{' '}
                  {s.truncated && s.total != null
                    ? t('composer.sample.showing', { shown: s.rows.toLocaleString(), total: s.total.toLocaleString() })
                    : t('composer.sample.rows', { count: s.rows })}
                  {s.adapterDiscarded ? ` · ${t('composer.sample.discarded', { count: s.adapterDiscarded })}` : ''}
                  {s.dangling ? ` · ${t('composer.sample.dangling', { count: s.dangling })}` : ''}
                </li>
              ))}
            </ul>
          ) : null}
          {item?.warnings.length ? (
            <ul className="mb-2 flex flex-col gap-0.5 text-xs text-warning">
              {item.warnings.map((w, i) => (
                <li key={i}>
                  <span className="font-mono">{w.fieldPath}</span> {w.message}
                </li>
              ))}
            </ul>
          ) : null}
          {item?.error ? (
            <p role="alert" className="text-xs text-danger">
              {item.error.fieldPath ? <span className="font-mono">{item.error.fieldPath} </span> : null}
              {item.error.message}
            </p>
          ) : null}
          <div className={reference && showRef ? 'grid grid-cols-2 gap-3' : undefined}>
            <div className="min-w-0">
              {reference && showRef ? <h3 className="mb-1 text-xs font-semibold uppercase tracking-wide text-muted">{t('composer.sample.generated')}</h3> : null}
              {item?.document ? (
                <pre className="font-mono text-xs leading-5">
                  <JsonNode value={item.document} path="" onPick={pick} selectedPath={selectedPath} />
                </pre>
              ) : !loading && !error && items.length === 0 && result ? (
                <p className="text-xs text-muted">{t('composer.sample.empty')}</p>
              ) : null}
            </div>
            {reference && showRef ? (
              <section aria-label={t('composer.sample.reference')} className="min-w-0 border-l border-border pl-3">
                <h3 className="mb-1 text-xs font-semibold uppercase tracking-wide text-muted">{t('composer.sample.reference')}</h3>
                {/* Differences are only worth listing when the mapping must match the design (OC-076). */}
                {item?.conformance && spec.referenceStrict ? <Conformance c={item.conformance} /> : null}
                <pre className="font-mono text-xs leading-5">
                  <JsonNode value={reference} path="" onPick={pick} selectedPath={selectedPath} />
                </pre>
              </section>
            ) : null}
          </div>
        </div>
      ) : null}
    </section>
  )
}

/**
 * How the sample document's shape differs from the customer's document (OC-061), one sentence
 * per difference with its fix from validation (OC-070, OC-071).
 */
function Conformance({ c }: { c: NonNullable<Item['conformance']> }) {
  const { t } = useTranslation()
  const issues = useWorkbench((s) => s.issues)
  const apply = useWorkbench((s) => s.apply)
  if (!c.missing.length && !c.extra.length && !c.typeDiff.length) {
    return <p className="mb-2 text-xs text-success">{t('composer.sample.conforms')}</p>
  }
  const issueFor = (code: string, path: string) => issues.find((i) => i.code === code && (i.nodePath || i.path) === path && i.fix)
  const row = (key: string, text: string, actions: ReactNode) => (
    <li key={key} className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
      <span>{text}</span>
      {actions}
    </li>
  )
  return (
    <div className="mb-2 text-xs">
      <p className="text-muted">{t('composer.sample.differs')}</p>
      <ul aria-label={t('composer.sample.conformance')} className="mt-1 flex flex-col gap-1 text-warning">
        {c.missing.map((p) => {
          const i = issueFor('REFERENCE_FIELD_UNMAPPED', p)
          return row(`m:${p}`, t('composer.sample.missingOne', { path: p }), i ? (
            <Button size="sm" variant="secondary" className="h-6" onClick={() => apply((s) => applyFix(s, i))}>
              {i.fix!.label}
            </Button>
          ) : null)
        })}
        {c.typeDiff.map((p) => row(`t:${p}`, t('composer.sample.typeDiffOne', { path: p }), null))}
        {c.extra.map((p) => {
          const i = issueFor('REFERENCE_EXTRA_FIELD', p)
          return row(`e:${p}`, t('composer.sample.extraOne', { path: p }), i?.fieldId ? (
            <>
              <Button size="sm" variant="secondary" className="h-6" onClick={() => apply((s) => addToDesign(s, i.fieldId!))}>
                {i.fix!.label}
              </Button>
              <Button size="sm" variant="ghost" className="h-6" aria-label={t('validation.removeFor', { path: p })} onClick={() => apply((s) => removeBranch(s, i.fieldId!))}>
                {t('validation.remove')}
              </Button>
            </>
          ) : null)
        })}
      </ul>
    </div>
  )
}

/** A JSON value whose members select their design field (paths like items[].product.sku). */
function JsonNode({ value, path, onPick, selectedPath, indent = 0 }: { value: unknown; path: string; onPick: (path: string) => void; selectedPath?: string; indent?: number }): ReactNode {
  const pad = '  '.repeat(indent + 1)
  const end = '  '.repeat(indent)
  if (Array.isArray(value)) {
    if (value.length === 0) return '[]'
    return (
      <>
        {'[\n'}
        {value.map((v, i) => (
          <span key={i}>
            {pad}
            <JsonNode value={v} path={`${path}[]`} onPick={onPick} selectedPath={selectedPath} indent={indent + 1} />
            {i < value.length - 1 ? ',\n' : '\n'}
          </span>
        ))}
        {end + ']'}
      </>
    )
  }
  if (value && typeof value === 'object') {
    const entries = Object.entries(value as Record<string, unknown>)
    if (entries.length === 0) return '{}'
    return (
      <>
        {'{\n'}
        {entries.map(([k, v], i) => {
          const p = path ? `${path}.${k}` : k
          return (
            <span key={k}>
              {pad}
              <button type="button" onClick={() => onPick(p)} className={cn('rounded text-primary-soft-foreground hover:underline', selectedPath === p && 'bg-primary-soft')}>
                {JSON.stringify(k)}
              </button>
              {': '}
              <JsonNode value={v} path={p} onPick={onPick} selectedPath={selectedPath} indent={indent + 1} />
              {i < entries.length - 1 ? ',\n' : '\n'}
            </span>
          )
        })}
        {end + '}'}
      </>
    )
  }
  return <span className={typeof value === 'string' ? 'text-success' : 'text-foreground'}>{JSON.stringify(value)}</span>
}
