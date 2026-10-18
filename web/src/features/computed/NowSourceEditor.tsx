import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Input } from '@/components/ui/input'
import { fieldsById } from '@/features/mapping/embeds'
import { dateFormats, fits, formatLabel, formatSample, nowOf, setNow, type NowArgs } from '@/features/mapping/nowSource'
import { useWorkbench } from '@/features/mapping/store'
import { cn } from '@/lib/utils'

const FORMATS = ['epoch_ms', 'epoch_s', 'iso8601', 'date', 'custom'] as const
const key = (a: NowArgs) => `${a.format ?? 'iso8601'}|${a.pattern ?? ''}|${a.timezone ?? ''}`

/**
 * The current date and time as a field's value (OC-179): when (the run's start or each
 * document's write), the format (the same as another date of the document, or one of
 * formatDatetime's), and a live sample of what's written.
 */
export function NowSourceEditor({ fieldId }: { fieldId: string }) {
  const { t } = useTranslation()
  const spec = useWorkbench((s) => s.history.present)
  const apply = useWorkbench((s) => s.apply)
  const [at] = useState(() => new Date())
  const formatId = useId()
  const args = nowOf(spec, fieldId)
  if (!args) return null
  const field = fieldsById(spec).get(fieldId)
  const refs = dateFormats(spec, fieldId)
  const same = refs.find((r) => key(r.args) === key(args))
  const set = (next: NowArgs) => apply((s) => setNow(s, fieldId, next))
  const epoch = args.format === 'epoch_ms' || args.format === 'epoch_s'
  const sample = formatSample(args, at)

  return (
    <section aria-label={t('now.title')} className="flex flex-col gap-2.5 text-xs">
      <div role="radiogroup" aria-label={t('now.when')} className="inline-flex w-fit rounded-[var(--radius-control)] border border-border bg-surface-muted p-0.5">
        {(['run', 'write'] as const).map((w) => (
          <button
            key={w}
            type="button"
            role="radio"
            aria-checked={args.at === w}
            title={t(`now.atHint.${w}`)}
            onClick={() => set({ ...args, at: w })}
            className={cn('rounded px-2.5 py-1', args.at === w ? 'bg-surface font-medium shadow-sm' : 'text-muted hover:text-foreground')}
          >
            {t(`now.at.${w}`)}
          </button>
        ))}
      </div>
      <label htmlFor={formatId} className="flex flex-col gap-1 font-medium text-muted">
        {t('now.format')}
        <select
          id={formatId}
          value={same ? `same:${same.fieldId}` : (args.format ?? 'iso8601')}
          onChange={(e) => {
            const v = e.target.value
            const ref = refs.find((r) => `same:${r.fieldId}` === v)
            set(ref ? { at: args.at, ...ref.args } : { at: args.at, format: v as NowArgs['format'], ...(v === 'custom' ? { pattern: args.pattern || 'yyyy-MM-dd HH:mm' } : {}), ...(args.timezone ? { timezone: args.timezone } : {}) })
          }}
          className="h-8 rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 text-[13px] font-normal text-foreground"
        >
          {refs.length ? (
            <optgroup label={t('now.sameAsGroup')}>
              {refs.map((r) => (
                <option key={r.fieldId} value={`same:${r.fieldId}`}>
                  {t('now.sameAs', { field: r.name, format: formatLabel(r.args) })}
                </option>
              ))}
            </optgroup>
          ) : null}
          <optgroup label={t('now.formatsGroup')}>
            {FORMATS.map((f) => (
              <option key={f} value={f}>
                {t(`now.formats.${f}`)}
              </option>
            ))}
          </optgroup>
        </select>
      </label>
      {args.format === 'custom' ? (
        <label className="flex flex-col gap-1 font-medium text-muted">
          {t('now.pattern')}
          <Input key={`p:${args.pattern}`} defaultValue={args.pattern} className="h-8 font-mono" onBlur={(e) => e.target.value.trim() && e.target.value !== args.pattern && set({ ...args, pattern: e.target.value.trim() })} />
        </label>
      ) : null}
      {!epoch ? (
        <label className="flex flex-col gap-1 font-medium text-muted">
          {t('now.timezone')}
          <Input
            key={`z:${args.timezone ?? ''}`}
            defaultValue={args.timezone ?? ''}
            placeholder="UTC"
            className="h-8 font-mono"
            onBlur={(e) => e.target.value.trim() !== (args.timezone ?? '') && set({ ...args, timezone: e.target.value.trim() || undefined })}
          />
        </label>
      ) : null}
      <p className="text-muted">
        {t('now.sample')} <code data-testid="now-sample" className="rounded bg-surface-muted px-1 font-mono text-foreground">{typeof sample === 'number' ? sample : JSON.stringify(sample)}</code>
      </p>
      {!fits(field?.type, args) ? (
        <p role="alert" className="text-warning">
          {t(epoch ? 'now.wantText' : 'now.wantEpoch', { field: field?.name ?? '', type: field?.type ?? '' })}
        </p>
      ) : null}
    </section>
  )
}
