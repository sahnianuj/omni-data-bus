import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { analyzeKey } from './analyzeKey'
import { previewMapping } from './api'
import { KeyTemplateInput } from './KeyTemplateInput'
import { renderKey } from './renderKey'
import { useWorkbench } from './store'

const verdictTone = { unique: 'success', 'may-collide': 'warning', 'not-idempotent': 'warning' } as const

/** Key & metadata panel at the top of the designer (OC-016). */
export function KeyPanel({ projectId, supportsExpiry = true }: { projectId: string; supportsExpiry?: boolean }) {
  const { t } = useTranslation()
  const spec = useWorkbench((s) => s.history.present)
  const columns = useWorkbench((s) => s.columns)
  const view = useWorkbench((s) => s.view)
  const issues = useWorkbench((s) => s.issues)
  const apply = useWorkbench((s) => s.apply)

  // Three sample rows, fetched once; keys re-render locally as the template changes.
  const sample = useQuery({
    queryKey: ['mapping-key-sample', view.id],
    queryFn: () => previewMapping(projectId, view.id, { limit: 3 }),
    retry: false,
    staleTime: 60_000,
  })
  const analysis = useMemo(() => analyzeKey(spec.key.template, columns), [spec.key.template, columns])
  const keys = useMemo(() => {
    if (!analysis.ok) return []
    return (sample.data?.items ?? []).map((it) => renderKey(analysis.segments, it.sourceRow, view.sourceTable))
  }, [analysis, sample.data, view.sourceTable])
  const keyIssues = issues.filter((i) => i.path === 'key' || i.path?.startsWith('key.') || i.path?.startsWith('typeField'))
  const typeCollision = spec.typeField.enabled && (spec.docSchema.root.children ?? []).some((f) => f.name === spec.typeField.name)

  return (
    <section aria-label={t('key.title')} className="flex flex-col gap-4 border-b border-border px-6 py-4">
      <div className="flex flex-col gap-1.5">
        <div className="flex items-center gap-2">
          <h3 className="text-sm font-semibold">{t('key.title')}</h3>
          {analysis.ok ? <Badge tone={verdictTone[analysis.verdict]}>{t(`key.verdict.${analysis.verdict}`)}</Badge> : null}
        </div>
        <KeyTemplateInput
          value={spec.key.template}
          columns={columns.filter((c) => !spec.ignoredColumns.includes(c.name))}
          onChange={(template) => apply((s) => ({ ...s, key: { ...s.key, template } }))}
          invalid={!analysis.ok || (analysis.ok && analysis.unknown.length > 0)}
          aria-describedby="key-help"
        />
        <p id="key-help" className="text-xs text-muted">
          {!analysis.ok
            ? analysis.error
            : analysis.unknown.length
              ? t('key.unknown', { columns: analysis.unknown.join(', ') })
              : t('key.help')}
        </p>
        {keys.length ? (
          <ul aria-label={t('key.preview')} className="flex flex-wrap gap-2">
            {keys.map((k, i) => (
              <li key={i} className={'key' in k ? 'rounded bg-surface-muted px-2 py-0.5 font-mono text-xs' : 'rounded bg-danger-soft px-2 py-0.5 text-xs text-danger'}>
                {'key' in k ? k.key : k.message}
              </li>
            ))}
          </ul>
        ) : null}
        {keyIssues.map((i) => (
          <p key={i.code + i.message} className={i.severity === 'error' ? 'text-xs text-danger' : 'text-xs text-warning'}>
            {i.message}
          </p>
        ))}
      </div>

      <div className="flex flex-wrap items-end gap-6 text-[13px]">
        <fieldset className="flex items-end gap-2">
          <legend className="sr-only">{t('key.typeField')}</legend>
          <label className="flex h-8 items-center gap-1.5">
            <input
              type="checkbox"
              checked={spec.typeField.enabled}
              onChange={(e) => apply((s) => ({ ...s, typeField: { ...s.typeField, enabled: e.target.checked } }))}
            />
            {t('key.typeField')}
          </label>
          <label className="flex flex-col gap-1 text-xs text-muted">
            {t('key.typeName')}
            <input
              value={spec.typeField.name}
              disabled={!spec.typeField.enabled}
              aria-invalid={typeCollision || undefined}
              onChange={(e) => apply((s) => ({ ...s, typeField: { ...s.typeField, name: e.target.value } }))}
              className="h-8 w-28 rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 font-mono text-xs text-foreground"
            />
          </label>
          <label className="flex flex-col gap-1 text-xs text-muted">
            {t('key.typeValue')}
            <input
              value={spec.typeField.value}
              disabled={!spec.typeField.enabled}
              onChange={(e) => apply((s) => ({ ...s, typeField: { ...s.typeField, value: e.target.value } }))}
              className="h-8 w-36 rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 font-mono text-xs text-foreground"
            />
          </label>
        </fieldset>
        <fieldset className="flex items-center gap-3">
          <legend className="mb-1 text-xs text-muted">{t('key.nulls')}</legend>
          {(['include', 'omit'] as const).map((n) => (
            <label key={n} className="flex items-center gap-1.5">
              <input type="radio" name="nullHandling" checked={spec.nullHandling === n} onChange={() => apply((s) => ({ ...s, nullHandling: n }))} />
              {t(`key.null.${n}`)}
            </label>
          ))}
        </fieldset>
        <label className="flex items-center gap-1.5 self-end pb-1.5" title={t('key.includeUnmappedHint')}>
          <input type="checkbox" checked={!spec.omitUnmapped} onChange={(e) => apply((s) => ({ ...s, omitUnmapped: !e.target.checked }))} />
          {t('key.includeUnmapped')}
        </label>
        {/* Document expiry exists only on kinds that support it (OC-036): not in a directory. */}
        {supportsExpiry ? (
        <label className="flex flex-col gap-1 text-xs text-muted">
          {t('key.expiry')}
          <input
            type="number"
            min={0}
            value={spec.expirySec ?? ''}
            placeholder={t('key.noExpiry')}
            onChange={(e) =>
              apply((s) => {
                const n = e.target.value === '' ? undefined : Math.max(0, Math.floor(Number(e.target.value)))
                const next = { ...s }
                delete next.expirySec
                return n ? { ...next, expirySec: n } : next
              })
            }
            className="h-8 w-32 rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 font-mono text-xs text-foreground"
          />
        </label>
        ) : null}
      </div>
      {typeCollision ? <p className="text-xs text-danger">{t('key.typeCollision', { name: spec.typeField.name })}</p> : null}
    </section>
  )
}
