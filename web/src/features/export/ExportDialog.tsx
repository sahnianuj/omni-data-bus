import { zodResolver } from '@hookform/resolvers/zod'
import { Check, Copy, Download, KeyRound, Lock, Sparkles, TriangleAlert } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { z } from 'zod'
import { ErrorState } from '@/components/ErrorState'
import { FormField } from '@/components/FormField'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useMappings } from '@/features/mapping/api'
import type { Project } from '@/features/projects/api'
import { formatLocation } from '@/features/targets/kinds'
import { ApiError } from '@/lib/api/client'
import { cn } from '@/lib/utils'
import { saveFile, useCreateExport, useExportOptions, useGeneratePassphrase, useStepUp, type ExportFile, type ExportRequest } from './api'
import { MIN_PASSPHRASE, passphraseProblem } from './passphrase'

const STEPS = ['project', 'passphrase', 'runtime'] as const
type StepName = (typeof STEPS)[number]

/** The bundled node of the all-in-one image (OC-072): unreachable outside its container. */
const BUNDLED = 'Bundled Couchbase'

const schema = z.object({
  passphrase: z.string(),
  runtime: z.enum(['script', 'bundle']),
  scriptRuntime: z.enum(['auto', 'binary', 'docker', 'embedded']),
  platform: z.string(),
  readers: z.string(),
  writers: z.number().int().min(1).max(512),
  batchSize: z.number().int().min(100).max(10000),
  writeMode: z.enum(['upsert', 'insert', 'replace']),
  maxErrors: z.number().int().min(-1),
})
export type ExportValues = z.infer<typeof schema>

/** The request the dialog sends (exported for tests): only how to package the project. */
export function toRequest(v: ExportValues): ExportRequest {
  const req: ExportRequest = {
    passphrase: v.passphrase,
    runtime: v.runtime,
    jobOptions: {
      readParallelism: v.readers,
      writeConcurrency: v.writers,
      batchSize: v.batchSize,
      writeMode: v.writeMode,
      errorPolicy: { maxErrors: v.maxErrors, maxErrorRatio: 1, minRowsForRatio: 0 },
    },
  }
  if (v.runtime === 'script') {
    const [os, arch] = v.platform.split('_')
    req.script = {
      runtime: v.scriptRuntime,
      ...(v.scriptRuntime === 'embedded' && os && arch ? { os: os as 'linux' | 'darwin', arch: arch as 'amd64' | 'arm64' } : {}),
    }
  }
  return req
}

function Choice({ name, value, current, title, hint, onSelect }: {
  name: string
  value: string
  current: string
  title: string
  hint?: string
  onSelect: (v: string) => void
}) {
  return (
    <label className={cn('flex cursor-pointer items-start gap-2.5 rounded-md border px-3 py-2 text-[13px]', current === value ? 'border-primary bg-primary-soft' : 'border-border')}>
      <input type="radio" name={name} value={value} checked={current === value} onChange={() => onSelect(value)} className="mt-0.5" />
      <span className="flex flex-col">
        <span className="font-medium">{title}</span>
        {hint ? <span className="text-xs text-muted">{hint}</span> : null}
      </span>
    </label>
  )
}

/** One read-only part of the project in the summary, with a link to the step that changes it. */
function Part({ title, to, change, children }: { title: string; to: string; change: string; children: ReactNode }) {
  return (
    <section aria-label={title} className="flex items-start gap-3 rounded-md border border-border px-3 py-2 text-[13px]">
      <Lock aria-hidden className="mt-0.5 size-3.5 shrink-0 text-subtle" />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="text-xs font-medium text-muted">{title}</span>
        {children}
      </div>
      <Link to={to} className="shrink-0 text-xs text-primary-soft-foreground hover:underline">{change}</Link>
    </section>
  )
}

/**
 * The guided export (OC-055). It exports the project as configured — its source, target, saved
 * credentials (sealed with a passphrase) and every ready mapping — and only asks how to package
 * and run it. To change the project, users go back to its steps (user decision, 2026-10-02).
 */
export function ExportDialog({ project, open, onOpenChange }: { project: Project; open: boolean; onOpenChange: (o: boolean) => void }) {
  const { t } = useTranslation()
  const mappings = useMappings(project.id)
  const options = useExportOptions()
  const create = useCreateExport(project.id)
  const stepUp = useStepUp()
  const generate = useGeneratePassphrase()
  const [step, setStep] = useState(0)
  const [stepError, setStepError] = useState<string | null>(null)
  const [password, setPassword] = useState<string | null>(null) // non-null: the server asked to confirm
  const [generated, setGenerated] = useState(false)
  const [done, setDone] = useState<{ file: ExportFile; passphrase: string } | null>(null)
  const [copied, setCopied] = useState(false)
  const form = useForm<ExportValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      passphrase: '', runtime: 'script', scriptRuntime: 'auto', platform: 'linux_amd64',
      readers: 'auto', writers: 64, batchSize: 1000, writeMode: 'upsert', maxErrors: 1000,
    },
  })
  const v = useWatch({ control: form.control }) as ExportValues
  const set = <K extends keyof ExportValues>(k: K, val: ExportValues[K]) => form.setValue(k, val as never, { shouldDirty: true })
  const name: StepName = STEPS[step] ?? 'project'
  const opts = options.data
  const all = mappings.data ?? []
  const ready = all.filter((m) => m.status === 'ready')
  const bundled = project.target?.name === BUNDLED
  const base = `/projects/${project.id}`

  /** Checks a step; returns an error message, or null. */
  const check = (s: StepName): string | null => {
    switch (s) {
      case 'project':
        if (bundled) return t('export.err.bundled')
        return ready.length === 0 ? t('export.err.noMappings') : null
      case 'passphrase': {
        const p = passphraseProblem(v.passphrase)
        return p ? t(`export.err.passphrase_${p}`, { min: MIN_PASSPHRASE }) : null
      }
      case 'runtime':
        return v.runtime === 'script' && v.scriptRuntime === 'embedded' && !(opts?.embeddable ?? []).includes(v.platform) ? t('export.err.platform') : null
    }
  }

  const next = () => {
    const err = check(name)
    setStepError(err)
    if (!err) setStep((s) => Math.min(s + 1, STEPS.length - 1))
  }

  const submit = async () => {
    for (const s of STEPS) {
      const err = check(s)
      if (err) {
        setStep(STEPS.indexOf(s))
        setStepError(err)
        return
      }
    }
    setStepError(null)
    try {
      if (password !== null) await stepUp.mutateAsync(password)
      const file = await create.mutateAsync(toRequest(v))
      saveFile(file.blob, file.fileName)
      setPassword(null)
      setDone({ file, passphrase: v.passphrase })
    } catch (e) {
      if (e instanceof ApiError && e.code === 'STEP_UP_REQUIRED') setPassword('')
    }
  }

  const close = (o: boolean) => {
    if (!o) {
      setStep(0)
      setDone(null)
      setPassword(null)
      setGenerated(false)
      setStepError(null)
      create.reset()
      stepUp.reset()
      form.reset()
    }
    onOpenChange(o)
  }

  const onGenerate = async () => {
    set('passphrase', await generate.mutateAsync())
    setGenerated(true)
  }

  if (done) {
    return (
      <Dialog open={open} onOpenChange={close}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>{t('export.doneTitle')}</DialogTitle>
            <DialogDescription>{t('export.doneBody', { file: done.file.fileName })}</DialogDescription>
          </DialogHeader>
          <p className="font-mono text-xs text-muted">{t('export.sha', { sha: done.file.bundleSha256 })}</p>
          <div role="note" className="flex flex-col gap-2 rounded-md border border-warning/40 bg-warning-soft px-3 py-2 text-[13px]">
            <p className="font-medium">{t('export.passphraseOnce')}</p>
            <div className="flex items-center gap-2">
              <code className="flex-1 break-all font-mono text-xs">{done.passphrase}</code>
              <Button
                size="sm"
                variant="secondary"
                onClick={() => {
                  void navigator.clipboard?.writeText(done.passphrase)
                  setCopied(true)
                }}
              >
                {copied ? <Check aria-hidden /> : <Copy aria-hidden />}
                {copied ? t('export.copied') : t('export.copy')}
              </Button>
            </div>
          </div>
          <DialogFooter>
            <Button onClick={() => close(false)}>{t('common.close')}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    )
  }

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t('export.title')}</DialogTitle>
          <DialogDescription>{t('export.intro')}</DialogDescription>
        </DialogHeader>
        <ol className="flex items-center gap-1 text-xs" aria-label={t('export.steps')}>
          {STEPS.map((s, i) => (
            <li key={s}>
              <button
                type="button"
                aria-current={i === step ? 'step' : undefined}
                onClick={() => i < step && setStep(i)}
                className={cn('rounded-full px-2.5 py-1', i === step ? 'bg-primary-soft font-medium text-primary-soft-foreground' : 'text-muted', i < step && 'hover:underline')}
              >
                {i + 1}. {t(`export.step.${s}`)}
              </button>
            </li>
          ))}
        </ol>
        <div className="flex max-h-[55vh] flex-col gap-2 overflow-y-auto pr-1">
          {name === 'project' ? (
            <>
              <p className="text-xs text-muted">{t('export.locked')}</p>
              <Part title={t('export.part.source')} to={`${base}/source`} change={t('export.change', { step: t('wizard.source') })}>
                <span>{project.source ? `${t(`projects.types.${project.source.type}`)} · ${project.source.name}` : '—'}</span>
              </Part>
              <Part title={t('export.part.target')} to={`${base}/target`} change={t('export.change', { step: t('wizard.target') })}>
                <span>{project.target ? `${t(`projects.types.${project.target.type}`)} · ${project.target.name}` : '—'}</span>
                {project.target?.type === 'directory' ? <span className="text-xs text-muted">{t('export.part.targetPath')}</span> : null}
                {bundled ? (
                  <span role="alert" className="flex items-center gap-1.5 text-xs text-danger">
                    <TriangleAlert aria-hidden className="size-3.5" />
                    {t('export.err.bundled')}
                  </span>
                ) : null}
              </Part>
              <Part title={t('export.part.credentials')} to={`${base}/source`} change={t('export.change', { step: t('wizard.source') })}>
                <span>{t('export.part.credentialsBody')}</span>
              </Part>
              <Part title={t('export.part.mappings', { ready: ready.length, count: all.length })} to={`${base}/mappings`} change={t('export.change', { step: t('wizard.mappings') })}>
                <ul className="flex flex-col gap-0.5">
                  {all.map((m) => (
                    <li key={m.id} className={cn('flex items-center gap-2', m.status !== 'ready' && 'text-muted')}>
                      <span className="font-medium">{m.sourceSchema}.{m.sourceTable}</span>
                      <span className="font-mono text-xs text-muted">→ {formatLocation(m.location)}</span>
                      {m.status === 'ready' ? null : (
                        <Badge tone="warning" title={m.firstError?.message}>{t('export.notIncluded')}</Badge>
                      )}
                    </li>
                  ))}
                </ul>
              </Part>
            </>
          ) : null}

          {name === 'passphrase' ? (
            <div className="flex flex-col gap-2">
              <p className="text-[13px] text-muted">{t('export.sealIntro')}</p>
              <div className="flex items-end gap-2">
                <div className="flex-1">
                  <FormField id="export-pass" label={t('export.cred.passphrase')} hint={generated ? t('export.cred.generatedHint') : t('export.cred.passphraseHint', { min: MIN_PASSPHRASE })}>
                    <Input id="export-pass" type={generated ? 'text' : 'password'} autoComplete="new-password" value={v.passphrase}
                      onChange={(e) => { set('passphrase', e.target.value); setGenerated(false) }} className="font-mono" />
                  </FormField>
                </div>
                <Button type="button" variant="secondary" loading={generate.isPending} onClick={() => void onGenerate()} className="mb-5">
                  <Sparkles aria-hidden />
                  {t('export.cred.generate')}
                </Button>
              </div>
            </div>
          ) : null}

          {name === 'runtime' ? (
            <div className="flex flex-col gap-2">
              <Choice name="runtime" value="script" current={v.runtime} onSelect={(x) => set('runtime', x as ExportValues['runtime'])} title={t('export.run.script')} hint={t('export.run.scriptHint')} />
              {v.runtime === 'script' ? (
                <div className="flex flex-wrap items-end gap-3 pl-6">
                  <FormField id="export-rt" label={t('export.run.runtime')}>
                    <select id="export-rt" title={t(`export.run.rtHint.${v.scriptRuntime}`)} value={v.scriptRuntime} onChange={(e) => set('scriptRuntime', e.target.value as ExportValues['scriptRuntime'])} className="h-9 rounded-md border border-border bg-surface px-2 text-[13px]">
                      {(['auto', 'binary', 'docker', 'embedded'] as const).map((r) => (
                        <option key={r} value={r}>{t(`export.run.rt.${r}`)}</option>
                      ))}
                    </select>
                  </FormField>
                  {v.scriptRuntime === 'embedded' ? (
                    <FormField id="export-platform" label={t('export.run.platform')}>
                      <select id="export-platform" value={v.platform} onChange={(e) => set('platform', e.target.value)} className="h-9 rounded-md border border-border bg-surface px-2 text-[13px]">
                        {['linux_amd64', 'linux_arm64', 'darwin_amd64', 'darwin_arm64'].map((p) => (
                          <option key={p} value={p} disabled={!(opts?.embeddable ?? []).includes(p)}>{p.replace('_', ' / ')}</option>
                        ))}
                      </select>
                    </FormField>
                  ) : null}
                  {v.scriptRuntime === 'auto' && opts?.autoEmbed ? (
                    <p className="mb-2.5 text-xs text-muted">{t('export.run.autoEmbed', { platform: opts.autoEmbed.replace('_', ' / ') })}</p>
                  ) : null}
                </div>
              ) : null}
              <Choice name="runtime" value="bundle" current={v.runtime} onSelect={(x) => set('runtime', x as ExportValues['runtime'])} title={t('export.run.bundle')} hint={t('export.run.bundleHint')} />
              <fieldset className="mt-1 grid grid-cols-5 gap-2">
                <legend className="mb-1 text-xs font-medium text-muted">{t('export.run.options')}</legend>
                <FormField id="export-readers" label={t('export.run.readers')}>
                  <select id="export-readers" value={v.readers} onChange={(e) => set('readers', e.target.value)} className="h-9 rounded-md border border-border bg-surface px-2 text-[13px]">
                    {['auto', '1', '2', '3', '4', '5', '6', '7', '8'].map((r) => <option key={r} value={r}>{r}</option>)}
                  </select>
                </FormField>
                <FormField id="export-writers" label={t('export.run.writers')}>
                  <Input id="export-writers" type="number" min={1} max={512} value={v.writers} onChange={(e) => set('writers', Number(e.target.value))} />
                </FormField>
                <FormField id="export-batch" label={t('export.run.batch')}>
                  <Input id="export-batch" type="number" min={100} max={10000} step={100} value={v.batchSize} onChange={(e) => set('batchSize', Number(e.target.value))} />
                </FormField>
                <FormField id="export-mode" label={t('export.run.writeMode')}>
                  <select id="export-mode" value={v.writeMode} onChange={(e) => set('writeMode', e.target.value as ExportValues['writeMode'])} className="h-9 rounded-md border border-border bg-surface px-2 text-[13px]">
                    {(['upsert', 'insert', 'replace'] as const).map((m) => <option key={m} value={m}>{t(`jobOptions.${m}`)}</option>)}
                  </select>
                </FormField>
                <FormField id="export-maxerr" label={t('export.run.maxErrors')}>
                  <Input id="export-maxerr" type="number" min={-1} value={v.maxErrors} onChange={(e) => set('maxErrors', Number(e.target.value))} />
                </FormField>
              </fieldset>
            </div>
          ) : null}

          {stepError && !(name === 'project' && bundled) ? <p role="alert" className="text-xs text-danger">{stepError}</p> : null}
          {password !== null ? (
            <div className="flex flex-col gap-1.5 rounded-md border border-primary/30 bg-primary-soft px-3 py-2">
              <FormField id="export-stepup" label={<span className="flex items-center gap-1.5"><KeyRound aria-hidden className="size-3.5" />{t('export.stepUp')}</span>} hint={t('export.stepUpHint')}>
                <Input id="export-stepup" type="password" autoComplete="current-password" autoFocus value={password} onChange={(e) => setPassword(e.target.value)} />
              </FormField>
            </div>
          ) : null}
          {stepUp.error ? <ErrorState error={stepUp.error} /> : null}
          {create.error && !(create.error instanceof ApiError && create.error.code === 'STEP_UP_REQUIRED') ? <ErrorState error={create.error} /> : null}
        </div>
        <DialogFooter>
          <Button variant="secondary" onClick={() => (step === 0 ? close(false) : setStep(step - 1))}>
            {step === 0 ? t('common.cancel') : t('export.back')}
          </Button>
          {step < STEPS.length - 1 ? (
            <Button onClick={next} disabled={name === 'project' && (bundled || ready.length === 0)}>{t('export.next')}</Button>
          ) : (
            <Button loading={create.isPending || stepUp.isPending} disabled={password === ''} onClick={() => void submit()}>
              <Download aria-hidden />
              {password !== null ? t('export.confirmAndExport') : t('export.export')}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
