import { Plus } from 'lucide-react'
import { useId, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { FormField } from '@/components/FormField'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Tooltip } from '@/components/ui/tooltip'
import { ApiError } from '@/lib/api/client'
import { parseDuration } from '@/lib/parseDuration'
import { cn } from '@/lib/utils'
import { useCreateKeyspace, type CreateOutcome } from './api'
import { bucketSpecSchema, MAX_EXPIRY_SEC, validScopeName, type BucketSpec } from './names'

/** A "+ New …" trigger that is disabled with the reason as a tooltip when the credential can't create. */
function NewTrigger({ label, disabledReason, ...props }: { label: string; disabledReason?: string } & React.ComponentProps<typeof Button>) {
  const btn = (
    <Button variant="link" size="sm" className="h-7 self-start px-0" disabled={!!disabledReason} {...props}>
      <Plus aria-hidden />
      {label}
    </Button>
  )
  // A disabled button gets no pointer events, so the tooltip wraps a focusable span.
  return disabledReason ? (
    <Tooltip content={disabledReason}>
      <span tabIndex={0} aria-label={`${label}: ${disabledReason}`} className="self-start">
        {btn}
      </span>
    </Tooltip>
  ) : (
    btn
  )
}

function useAnnounce() {
  const { t } = useTranslation()
  return (kind: 'bucket' | 'scope' | 'collection', name: string, outcome: CreateOutcome) => {
    const what = t(`targets.kind.${kind}`)
    if (outcome === 'exists') toast.info(t('targets.existsSelected', { what, name }))
    else if (outcome === 'pending') toast.info(t('targets.pending', { what, name }))
    else toast.success(t('targets.created', { what, name }))
  }
}

type NameProps = {
  connId: string
  kind: 'scope' | 'collection'
  bucket: string
  scope?: string
  disabledReason?: string
  /** Offer "Create when migration starts" (the plan-then-create option, OC-014 AC5). */
  allowDeferred?: boolean
  onDone: (name: string, deferred: boolean) => void
}

/** "+ New scope" / "+ New collection": an inline popover with name (and max TTL for collections). */
export function NewNamePopover({ connId, kind, bucket, scope = '', disabledReason, allowDeferred, onDone }: NameProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [ttl, setTtl] = useState('')
  const [deferred, setDeferred] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const create = useCreateKeyspace(connId)
  const announce = useAnnounce()
  const id = useId()
  const label = t(kind === 'scope' ? 'targets.newScope' : 'targets.newCollection')
  const canCreate = !disabledReason
  // Without create rights the only option is to plan the name for the migration to create.
  const defer = deferred || !canCreate

  const submit = (e: FormEvent) => {
    e.preventDefault()
    setError(null)
    if (!validScopeName(name)) return setError(t('targets.errors.scopeName'))
    const seconds = ttl.trim() === '' ? 0 : parseDuration(ttl)
    if (seconds === null || seconds > MAX_EXPIRY_SEC) return setError(t('targets.errors.ttl'))
    if (defer) {
      onDone(name, true)
      setOpen(false)
      return
    }
    const req = kind === 'scope' ? { kind, bucket, name } : { kind, bucket, scope, name, maxExpirySec: seconds }
    create.mutate(req, {
      onSuccess: (r) => {
        announce(kind, name, r.outcome)
        onDone(name, false)
        setOpen(false)
        setName('')
        setTtl('')
      },
      onError: (err) => setError(err instanceof ApiError ? (err.detail ?? err.title) : String(err)),
    })
  }

  if (!canCreate && !allowDeferred) return <NewTrigger label={label} disabledReason={disabledReason} />
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button variant="link" size="sm" className="h-7 self-start px-0">
          <Plus aria-hidden />
          {label}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-80">
        <form onSubmit={submit} noValidate className="flex flex-col gap-3" aria-label={label}>
          <FormField id={`${id}-name`} label={t('targets.name')} error={error ?? undefined}>
            <Input id={`${id}-name`} value={name} onChange={(e) => setName(e.target.value)} autoFocus invalid={!!error} />
          </FormField>
          {kind === 'collection' && !defer ? (
            <FormField id={`${id}-ttl`} label={t('targets.maxTtl')} hint={t('targets.maxTtlHint')}>
              <Input id={`${id}-ttl`} value={ttl} onChange={(e) => setTtl(e.target.value)} placeholder={t('targets.ttlPlaceholder')} />
            </FormField>
          ) : null}
          {allowDeferred ? (
            <label className="flex items-start gap-2 text-xs text-muted">
              <input type="checkbox" checked={defer} disabled={!canCreate} onChange={(e) => setDeferred(e.target.checked)} className="mt-0.5" />
              {t('targets.deferred')}
            </label>
          ) : null}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" size="sm" onClick={() => setOpen(false)}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" size="sm" loading={create.isPending}>
              {defer ? t('targets.useName') : create.isPending ? t('targets.creating') : t('targets.create')}
            </Button>
          </div>
        </form>
      </PopoverContent>
    </Popover>
  )
}

const defaults: BucketSpec = { name: '', bucketType: 'couchbase', storageBackend: 'magma', ramQuotaMB: 1024, numReplicas: 1 }

/** "+ New bucket": name, type, storage backend, RAM quota and replicas. */
export function NewBucketDialog({ connId, disabledReason, onDone }: { connId: string; disabledReason?: string; onDone: (name: string) => void }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [spec, setSpec] = useState<BucketSpec>(defaults)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [serverError, setServerError] = useState<string | null>(null)
  const create = useCreateKeyspace(connId)
  const announce = useAnnounce()
  const id = useId()
  const set = <K extends keyof BucketSpec>(k: K, v: BucketSpec[K]) => setSpec((s) => ({ ...s, [k]: v }))

  const submit = (e: FormEvent) => {
    e.preventDefault()
    setServerError(null)
    const r = bucketSpecSchema.safeParse(spec)
    if (!r.success) {
      setErrors(Object.fromEntries(r.error.issues.map((i) => [String(i.path[0]), t(i.message)])))
      return
    }
    setErrors({})
    create.mutate(
      { kind: 'bucket', ...spec },
      {
        onSuccess: (res) => {
          announce('bucket', spec.name, res.outcome)
          onDone(spec.name)
          setOpen(false)
          setSpec(defaults)
        },
        onError: (err) => setServerError(err instanceof ApiError ? (err.detail ?? err.title) : String(err)),
      },
    )
  }

  return (
    <>
      <NewTrigger label={t('targets.newBucket')} disabledReason={disabledReason} onClick={() => setOpen(true)} />
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('targets.newBucket')}</DialogTitle>
            <DialogDescription>{t('targets.newBucketBody')}</DialogDescription>
          </DialogHeader>
          <form onSubmit={submit} noValidate className="flex flex-col gap-4">
            <FormField id={`${id}-name`} label={t('targets.name')} error={errors.name}>
              <Input id={`${id}-name`} value={spec.name} onChange={(e) => set('name', e.target.value)} invalid={!!errors.name} autoFocus />
            </FormField>
            <fieldset className="flex flex-col gap-1.5">
              <legend className="mb-1.5 text-sm font-medium">{t('targets.bucketType')}</legend>
              <div className="grid grid-cols-2 gap-2">
                {(['couchbase', 'ephemeral'] as const).map((bt) => (
                  <label
                    key={bt}
                    className={cn('flex cursor-pointer flex-col gap-0.5 rounded-lg border p-3 text-sm', spec.bucketType === bt ? 'border-primary bg-primary-soft' : 'border-border')}
                  >
                    <span className="flex items-center gap-2 font-medium">
                      <input type="radio" name={`${id}-type`} checked={spec.bucketType === bt} onChange={() => set('bucketType', bt)} />
                      {t(`targets.type.${bt}`)}
                    </span>
                    <span className="text-xs text-muted">{t(`targets.typeHint.${bt}`)}</span>
                  </label>
                ))}
              </div>
            </fieldset>
            {spec.bucketType === 'couchbase' ? (
              <FormField id={`${id}-backend`} label={t('targets.storageBackend')} error={errors.storageBackend} hint={t('targets.backendHint')}>
                <select
                  id={`${id}-backend`}
                  value={spec.storageBackend}
                  onChange={(e) => set('storageBackend', e.target.value)}
                  className="h-9 rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 text-sm"
                >
                  <option value="magma">{t('targets.magma')}</option>
                  <option value="couchstore">{t('targets.couchstore')}</option>
                </select>
              </FormField>
            ) : null}
            <div className="grid grid-cols-2 gap-3">
              <FormField id={`${id}-ram`} label={t('targets.ramQuota')} error={errors.ramQuotaMB}>
                <Input id={`${id}-ram`} type="number" min={100} value={spec.ramQuotaMB} onChange={(e) => set('ramQuotaMB', Number(e.target.value))} invalid={!!errors.ramQuotaMB} />
              </FormField>
              <FormField id={`${id}-replicas`} label={t('targets.replicas')} error={errors.numReplicas}>
                <select
                  id={`${id}-replicas`}
                  value={spec.numReplicas}
                  onChange={(e) => set('numReplicas', Number(e.target.value))}
                  className="h-9 rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 text-sm"
                >
                  {[0, 1, 2, 3].map((n) => (
                    <option key={n} value={n}>
                      {n}
                    </option>
                  ))}
                </select>
              </FormField>
            </div>
            {serverError ? (
              <p role="alert" className="text-sm text-danger">
                {serverError}
              </p>
            ) : null}
            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => setOpen(false)}>
                {t('common.cancel')}
              </Button>
              <Button type="submit" loading={create.isPending}>
                {create.isPending ? t('targets.creatingBucket') : t('targets.create')}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  )
}
