import { zodResolver } from '@hookform/resolvers/zod'
import { ChevronRight } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'
import { cn } from '@/lib/utils'
import { defaultJobOptions, type JobOptions } from './api'

// Mirrors jobs.Options.Validate on the server (OC-023 AC3).
export const optionsSchema = z.object({
  writeMode: z.enum(['upsert', 'insert', 'replace']),
  batchSize: z.number({ error: 'Enter a number' }).int().min(100, 'At least 100').max(10000, 'At most 10,000'),
  readParallelism: z.string().refine((v) => v === 'auto' || (/^\d+$/.test(v) && Number(v) >= 1 && Number(v) <= 16), 'auto, or 1 to 16'),
  writeConcurrency: z.number({ error: 'Enter a number' }).int().min(1, 'At least 1').max(512, 'At most 512'),
  tableConcurrency: z.number({ error: 'Enter a number' }).int().min(1, 'At least 1').max(4, 'At most 4'),
  maxDocsPerSecond: z.number().int().min(1, 'At least 1, or empty for no limit').nullable(),
  durability: z.enum(['none', 'majority', 'majorityAndPersistActive', 'persistToMajority']),
  errorPolicy: z.object({
    maxErrors: z.number({ error: 'Enter a number' }).int().min(-1, '-1 (no limit) or more'),
    maxErrorRatio: z.number({ error: 'Enter a number' }).min(0, 'Between 0 and 1').max(1, 'Between 0 and 1'),
    minRowsForRatio: z.number({ error: 'Enter a number' }).int().min(0, '0 or more'),
  }),
  exactCount: z.enum(['auto', 'always', 'never']),
  verifySampleSize: z.number({ error: 'Enter a number' }).int().min(0, '0 or more').max(10000, 'At most 10,000'),
})

export type OptionValues = z.infer<typeof optionsSchema>

type Props = {
  /** Called with the options when valid, or null while a field is invalid. */
  onChange: (options: JobOptions | null) => void
}

// setValueAs also sees the default values (numbers, null), not only input strings.
const num = { setValueAs: (v: unknown) => (v === '' || v == null ? Number.NaN : Number(v)) }
const nullableNum = { setValueAs: (v: unknown) => (v === '' || v == null ? null : Number(v)) }
const inputCls = 'h-8 w-full rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 font-mono text-xs aria-[invalid=true]:border-danger'

/** The Review step's collapsible Advanced options, validated like the server. */
export function AdvancedOptions({ onChange }: Props) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const form = useForm<OptionValues>({ resolver: zodResolver(optionsSchema), defaultValues: defaultJobOptions, mode: 'onChange' })
  const values = useWatch({ control: form.control })
  const { errors } = form.formState

  useEffect(() => {
    const parsed = optionsSchema.safeParse(values)
    onChange(parsed.success ? parsed.data : null)
  }, [values, onChange])

  const field = (label: string, hint: string, control: React.ReactNode, error?: string) => (
    <label className="flex flex-col gap-1">
      <span className="text-xs font-medium">{label}</span>
      {control}
      {error ? <span className="text-xs text-danger">{error}</span> : <span className="text-[11px] text-subtle">{hint}</span>}
    </label>
  )
  const e = (msg?: string) => (msg ? { 'aria-invalid': true } : {})

  return (
    <section className="rounded-[var(--radius-card)] border border-border bg-surface">
      <button type="button" aria-expanded={open} onClick={() => setOpen((o) => !o)} className="flex w-full items-center gap-2 px-4 py-3 text-left text-sm font-semibold">
        <ChevronRight className={cn('size-4 transition-transform', open && 'rotate-90')} aria-hidden />
        {t('jobOptions.title')}
        {Object.keys(errors).length ? <span className="ml-2 text-xs font-normal text-danger">{t('jobOptions.fix')}</span> : null}
      </button>
      <div hidden={!open} className="grid grid-cols-1 gap-4 border-t border-border px-4 py-4 sm:grid-cols-2 lg:grid-cols-3">
        {field(t('jobOptions.writeMode'), t('jobOptions.writeModeHint'),
          <select {...form.register('writeMode')} aria-label={t('jobOptions.writeMode')} className={inputCls}>
            <option value="upsert">{t('jobOptions.upsert')}</option>
            <option value="insert">{t('jobOptions.insert')}</option>
            <option value="replace">{t('jobOptions.replace')}</option>
          </select>)}
        {field(t('jobOptions.batchSize'), t('jobOptions.batchSizeHint'),
          <input type="number" aria-label={t('jobOptions.batchSize')} {...e(errors.batchSize?.message)} {...form.register('batchSize', num)} className={inputCls} />,
          errors.batchSize?.message)}
        {field(t('jobOptions.readParallelism'), t('jobOptions.readParallelismHint'),
          <input aria-label={t('jobOptions.readParallelism')} {...e(errors.readParallelism?.message)} {...form.register('readParallelism')} className={inputCls} />,
          errors.readParallelism?.message)}
        {field(t('jobOptions.writeConcurrency'), t('jobOptions.writeConcurrencyHint'),
          <input type="number" aria-label={t('jobOptions.writeConcurrency')} {...e(errors.writeConcurrency?.message)} {...form.register('writeConcurrency', num)} className={inputCls} />,
          errors.writeConcurrency?.message)}
        {field(t('jobOptions.tableConcurrency'), t('jobOptions.tableConcurrencyHint'),
          <input type="number" aria-label={t('jobOptions.tableConcurrency')} {...e(errors.tableConcurrency?.message)} {...form.register('tableConcurrency', num)} className={inputCls} />,
          errors.tableConcurrency?.message)}
        {field(t('jobOptions.maxDocsPerSecond'), t('jobOptions.maxDocsPerSecondHint'),
          <input type="number" aria-label={t('jobOptions.maxDocsPerSecond')} placeholder={t('jobOptions.unlimited')} {...e(errors.maxDocsPerSecond?.message)} {...form.register('maxDocsPerSecond', nullableNum)} className={inputCls} />,
          errors.maxDocsPerSecond?.message)}
        {field(t('jobOptions.durability'), t('jobOptions.durabilityHint'),
          <select {...form.register('durability')} aria-label={t('jobOptions.durability')} className={inputCls}>
            <option value="none">{t('jobOptions.durabilityLevel.none')}</option>
            <option value="majority">{t('jobOptions.durabilityLevel.majority')}</option>
            <option value="majorityAndPersistActive">{t('jobOptions.durabilityLevel.majorityAndPersistActive')}</option>
            <option value="persistToMajority">{t('jobOptions.durabilityLevel.persistToMajority')}</option>
          </select>)}
        {field(t('jobOptions.maxErrors'), t('jobOptions.maxErrorsHint'),
          <input type="number" aria-label={t('jobOptions.maxErrors')} {...e(errors.errorPolicy?.maxErrors?.message)} {...form.register('errorPolicy.maxErrors', num)} className={inputCls} />,
          errors.errorPolicy?.maxErrors?.message)}
        {field(t('jobOptions.maxErrorRatio'), t('jobOptions.maxErrorRatioHint'),
          <input type="number" step="0.001" aria-label={t('jobOptions.maxErrorRatio')} {...e(errors.errorPolicy?.maxErrorRatio?.message)} {...form.register('errorPolicy.maxErrorRatio', num)} className={inputCls} />,
          errors.errorPolicy?.maxErrorRatio?.message)}
        {field(t('jobOptions.exactCount'), t('jobOptions.exactCountHint'),
          <select {...form.register('exactCount')} aria-label={t('jobOptions.exactCount')} className={inputCls}>
            <option value="auto">{t('jobOptions.count.auto')}</option>
            <option value="always">{t('jobOptions.count.always')}</option>
            <option value="never">{t('jobOptions.count.never')}</option>
          </select>)}
      </div>
    </section>
  )
}
