import { zodResolver } from '@hookform/resolvers/zod'
import { ChevronDown, FolderOpen, Zap } from 'lucide-react'
import { useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'
import { FormField } from '@/components/FormField'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FolderPicker } from '@/features/targets/FolderPicker'
import { documentPath } from '@/lib/pathcodec'
import { cn } from '@/lib/utils'
import type { ConnectionFormProps, ConnectionPayload } from './types'

type Values = {
  path: string
  createIfMissing: boolean
  layout: 'flat' | 'sharded' | 'key-path'
  pretty: boolean
  compression: 'none' | 'gzip'
  fileMode: string
  durability: 'none' | 'file' | 'file+dir'
  manifest: boolean
  maxDocMiB: string
}

/** Where a document of the orders table lands, for the live example (OC-039). */
function examplePath(root: string, layout: Values['layout'], gzip: boolean): string {
  const key = layout === 'key-path' ? '2026::10::1001' : '1001'
  return `${root.replace(/\/+$/, '')}/${documentPath(layout, 'orders', key, gzip)}`
}

const selectCls = 'h-9 w-full rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 text-sm'

/** Directory target form (OC-038, architecture §6.2). No credentials: the server writes as itself. */
export function DirectoryForm({ existing, testing, saving, onTest, onSave, onCancel }: ConnectionFormProps) {
  const { t } = useTranslation()
  const cfg = (existing?.config ?? {}) as Record<string, unknown>
  const [advanced, setAdvanced] = useState(false)
  const schema = z.object({
    path: z.string().trim().regex(/^\//, t('dir.errors.path')),
    createIfMissing: z.boolean(),
    layout: z.enum(['flat', 'sharded', 'key-path']),
    pretty: z.boolean(),
    compression: z.enum(['none', 'gzip']),
    fileMode: z.string().refine((v) => /^0[0-7]{3}$/.test(v) && parseInt(v, 8) >= 0o600 && parseInt(v, 8) <= 0o666, t('dir.errors.fileMode')),
    durability: z.enum(['none', 'file', 'file+dir']),
    manifest: z.boolean(),
    maxDocMiB: z.string().refine((v) => /^\d+(\.\d+)?$/.test(v) && +v * 1048576 >= 1024 && +v <= 1024, t('dir.errors.maxDoc')),
  })
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: {
      path: String(cfg.path ?? ''),
      createIfMissing: cfg.createIfMissing === true,
      layout: (cfg.layout as Values['layout']) ?? 'flat',
      pretty: cfg.pretty === true,
      compression: (cfg.compression as Values['compression']) ?? 'none',
      fileMode: String(cfg.fileMode ?? '0640'),
      durability: (cfg.durability as Values['durability']) ?? 'none',
      manifest: cfg.manifest !== false,
      maxDocMiB: String(Number(cfg.maxDocBytes ?? 20971520) / 1048576),
    },
  })
  const { errors } = form.formState
  const [picking, setPicking] = useState(false)
  const path = useWatch({ control: form.control, name: 'path' })
  const layout = useWatch({ control: form.control, name: 'layout' })
  const durability = useWatch({ control: form.control, name: 'durability' })
  const compression = useWatch({ control: form.control, name: 'compression' })
  const toPayload = (v: Values): ConnectionPayload => ({
    config: {
      path: v.path.trim(), createIfMissing: v.createIfMissing, layout: v.layout, pretty: v.pretty, compression: v.compression,
      fileMode: v.fileMode, durability: v.durability, manifest: v.manifest, maxDocBytes: Math.round(Number(v.maxDocMiB) * 1048576),
    },
    secrets: {},
  })
  return (
    <form className="flex flex-col gap-4" onSubmit={(e) => e.preventDefault()}>
      <FormField id="dir-path" label={t('dir.path')} hint={t('dir.pathHint')} error={errors.path?.message}>
        <div className="flex gap-2">
          <Input id="dir-path" placeholder={t('dir.pathPlaceholder')} className="font-mono" invalid={!!errors.path} {...form.register('path')} />
          <Button type="button" variant="secondary" onClick={() => setPicking(true)}>
            <FolderOpen aria-hidden />
            {t('dir.choose')}
          </Button>
        </div>
      </FormField>
      {picking ? (
        <FolderPicker
          open
          onOpenChange={setPicking}
          initialPath={path}
          onPick={(p) => form.setValue('path', p, { shouldValidate: true, shouldDirty: true })}
        />
      ) : null}
      {path ? (
        <p className="rounded-md bg-surface-muted px-3 py-2 font-mono text-xs text-muted">
          {t('dir.example', { key: layout === 'key-path' ? '2026::10::1001' : '1001', path: examplePath(path, layout, compression === 'gzip') })}
        </p>
      ) : null}
      <div className="grid grid-cols-2 gap-3">
        <FormField id="dir-layout" label={t('dir.layout')} hint={t(`dir.layoutHint.${layout}`)}>
          <select id="dir-layout" className={selectCls} {...form.register('layout')}>
            <option value="flat">{t('dir.layouts.flat')}</option>
            <option value="sharded">{t('dir.layouts.sharded')}</option>
            <option value="key-path">{t('dir.layouts.key-path')}</option>
          </select>
        </FormField>
        <FormField id="dir-durability" label={t('dir.durability')} hint={t(`dir.durabilityHint.${durability}`)}>
          <select id="dir-durability" className={selectCls} {...form.register('durability')}>
            <option value="none">{t('dir.durabilities.none')}</option>
            <option value="file">{t('dir.durabilities.file')}</option>
            <option value="file+dir">{t('dir.durabilities.file+dir')}</option>
          </select>
        </FormField>
      </div>
      <button type="button" aria-expanded={advanced} onClick={() => setAdvanced((v) => !v)} className="flex h-8 items-center justify-between text-[13px] text-muted">
        <span>{t('dir.advanced')}</span>
        <ChevronDown className={cn('size-3.5 transition-transform', advanced && 'rotate-180')} aria-hidden />
      </button>
      {advanced ? (
        <div className="grid grid-cols-2 gap-3">
          <FormField id="dir-compression" label={t('dir.compression')}>
            <select id="dir-compression" className={selectCls} {...form.register('compression')}>
              <option value="none">{t('dir.compressions.none')}</option>
              <option value="gzip">{t('dir.compressions.gzip')}</option>
            </select>
          </FormField>
          <FormField id="dir-mode" label={t('dir.fileMode')} error={errors.fileMode?.message}>
            <Input id="dir-mode" className="font-mono" invalid={!!errors.fileMode} {...form.register('fileMode')} />
          </FormField>
          <FormField id="dir-max" label={t('dir.maxDoc')} error={errors.maxDocMiB?.message}>
            <Input id="dir-max" inputMode="decimal" invalid={!!errors.maxDocMiB} {...form.register('maxDocMiB')} />
          </FormField>
          <div className="col-span-2 flex flex-col gap-2 text-sm">
            <label className="flex items-center gap-2">
              <input type="checkbox" {...form.register('createIfMissing')} />
              {t('dir.createIfMissing')}
            </label>
            <label className="flex items-center gap-2">
              <input type="checkbox" {...form.register('pretty')} />
              {t('dir.pretty')}
            </label>
            <label className="flex items-center gap-2">
              <input type="checkbox" {...form.register('manifest')} />
              {t('dir.manifest')}
            </label>
          </div>
        </div>
      ) : null}
      <div className="flex items-center justify-end gap-2 border-t border-border pt-4">
        {onCancel ? (
          <Button variant="ghost" onClick={onCancel}>
            {t('repo.cancelEdit')}
          </Button>
        ) : null}
        <Button variant="secondary" loading={testing} onClick={form.handleSubmit((v) => onTest(toPayload(v)))}>
          <Zap aria-hidden />
          {t('repo.test')}
        </Button>
        <Button loading={saving} onClick={form.handleSubmit((v) => onSave(toPayload(v)))}>
          {t('repo.save')}
        </Button>
      </div>
    </form>
  )
}
