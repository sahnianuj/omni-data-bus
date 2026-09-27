import { useQuery } from '@tanstack/react-query'
import { CheckCircle2, Copy, RefreshCw, XCircle } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { formatBytes } from '@/lib/format'
import { cn } from '@/lib/utils'
import { previewMapping } from './api'
import { selectionForPath } from './fixes'
import { useWorkbench } from './store'
import type { MappingPreview } from './types'

type Item = NonNullable<MappingPreview['items']>[number]

const show = (v: unknown) => (v === undefined ? '—' : JSON.stringify(v))

function ItemDetail({ item, writeMode, onJump }: { item: Item; writeMode: 'upsert' | 'insert'; onJump: (path: string | undefined) => void }) {
  const { t } = useTranslation()
  const json = JSON.stringify(item.document ?? null, null, 2)
  return (
    <div className="flex min-w-0 flex-1 flex-col gap-2">
      {item.error ? (
        <div role="alert" className="rounded-md bg-danger-soft p-3 text-[13px] text-danger">
          <p className="font-medium">
            {item.error.fieldPath ? <span className="font-mono">{item.error.fieldPath}: </span> : null}
            {item.error.message}
          </p>
          <p className="font-mono text-[11px]">{item.error.code}</p>
          {item.error.fieldPath && item.error.fieldPath !== 'key' ? (
            <Button size="sm" variant="link" className="h-6 px-0 text-danger" onClick={() => onJump(item.error?.fieldPath)}>
              {t('preview.jump')}
            </Button>
          ) : null}
        </div>
      ) : null}
      {item.keyExists ? (
        <p className="rounded-md bg-warning-soft px-3 py-1.5 text-xs text-warning">{t(writeMode === 'insert' ? 'preview.existsInsert' : 'preview.existsUpsert')}</p>
      ) : null}
      {item.warnings.map((w, i) => (
        <p key={i} className="text-xs text-warning">
          {w.fieldPath ? `${w.fieldPath}: ` : ''}
          {w.message}
        </p>
      ))}
      <Tabs defaultValue="document" className="flex min-h-0 flex-1 flex-col">
        <TabsList>
          <TabsTrigger value="document">{t('preview.document')}</TabsTrigger>
          <TabsTrigger value="source">{t('preview.sourceRow')}</TabsTrigger>
          <TabsTrigger value="trace">{t('preview.trace')}</TabsTrigger>
        </TabsList>
        <TabsContent value="document" className="relative min-h-0 flex-1 overflow-auto">
          {item.document ? (
            <>
              <Button
                size="icon"
                variant="ghost"
                className="absolute right-2 top-2 size-7"
                aria-label={t('preview.copy')}
                onClick={() => void navigator.clipboard?.writeText(json)}
              >
                <Copy aria-hidden />
              </Button>
              <pre className="rounded-md bg-surface-muted p-3 font-mono text-xs">{json}</pre>
            </>
          ) : (
            <p className="text-[13px] text-muted">{t('preview.noDocument')}</p>
          )}
        </TabsContent>
        <TabsContent value="source" className="min-h-0 flex-1 overflow-auto">
          <table className="w-full text-[13px]">
            <tbody>
              {Object.entries(item.sourceRow).map(([k, v]) => (
                <tr key={k} className="border-b border-border">
                  <th scope="row" className="py-1 pr-3 text-left font-mono text-xs font-normal text-muted">
                    {k}
                  </th>
                  <td className="py-1 font-mono text-xs">{show(v)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </TabsContent>
        <TabsContent value="trace" className="min-h-0 flex-1 overflow-auto">
          <ul aria-label={t('preview.trace')} className="flex flex-col gap-2">
            {Object.entries(item.trace ?? {}).map(([path, steps]) => (
              <li key={path} className="rounded-md border border-border p-2">
                <p className="mb-1 font-mono text-xs font-medium">{path}</p>
                <ol className="flex flex-wrap items-center gap-1.5 font-mono text-[11px]">
                  {steps.map((s, i) => (
                    <li key={i} className="flex items-center gap-1.5">
                      {i > 0 ? <span className="text-subtle">→</span> : null}
                      <span className="text-subtle">{s.step}</span>
                      <span>{show(s.value)}</span>
                    </li>
                  ))}
                </ol>
              </li>
            ))}
          </ul>
        </TabsContent>
      </Tabs>
    </div>
  )
}

/** Sample documents from the current (unsaved) spec, with errors, trace and key collisions (OC-022). */
export function PreviewDrawer({ projectId, open, onOpenChange }: { projectId: string; open: boolean; onOpenChange: (o: boolean) => void }) {
  const { t } = useTranslation()
  const spec = useWorkbench((s) => s.history.present)
  const mappingId = useWorkbench((s) => s.view.id)
  const select = useWorkbench((s) => s.select)
  const apply = useWorkbench((s) => s.apply)
  const [limit, setLimit] = useState(10)
  const [sampleMode, setSampleMode] = useState<'first' | 'random'>('first')
  const [checkKeys, setCheckKeys] = useState(false)
  const [writeMode] = useState<'upsert' | 'insert'>('upsert')
  const [index, setIndex] = useState(0)
  // The spec is captured on open/refresh rather than tracked, so typing elsewhere doesn't refetch.
  // `open` is controlled by the workbench's Preview button, so capture on every false → true
  // change here (Dialog.onOpenChange only fires for changes the dialog makes itself).
  const [snapshot, setSnapshot] = useState(spec)
  const [nonce, setNonce] = useState(0)
  const [wasOpen, setWasOpen] = useState(open)
  if (open !== wasOpen) {
    setWasOpen(open)
    if (open) {
      setSnapshot(spec)
      setNonce((n) => n + 1)
    }
  }

  const q = useQuery({
    queryKey: ['mapping-preview', mappingId, limit, sampleMode, checkKeys, writeMode, nonce, snapshot],
    queryFn: () => previewMapping(projectId, mappingId, { limit, sampleMode, checkKeys, writeMode, specOverride: snapshot }),
    enabled: open,
    retry: false,
    staleTime: Infinity,
  })
  const refresh = () => {
    setSnapshot(spec)
    setNonce((n) => n + 1)
  }
  const items = q.data?.items ?? []
  const item = items[Math.min(index, items.length - 1)]
  const sum = q.data?.summary

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
    >
      <DialogContent className="left-auto right-0 top-0 flex h-full w-full max-w-4xl translate-x-0 translate-y-0 flex-col rounded-none">
        <DialogHeader>
          <DialogTitle>{t('preview.title')}</DialogTitle>
          <DialogDescription>{t('preview.body')}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-wrap items-center gap-3 text-[13px]">
          <label className="flex items-center gap-1.5">
            {t('preview.sample')}
            <select value={limit} onChange={(e) => setLimit(Number(e.target.value))} className="h-8 rounded border border-border-strong bg-surface px-1.5">
              {[10, 25, 50].map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </label>
          <select aria-label={t('preview.mode')} value={sampleMode} onChange={(e) => setSampleMode(e.target.value as 'first' | 'random')} className="h-8 rounded border border-border-strong bg-surface px-1.5">
            <option value="first">{t('preview.first')}</option>
            <option value="random">{t('preview.random')}</option>
          </select>
          <label className="flex items-center gap-1.5">
            <input type="checkbox" checked={checkKeys} onChange={(e) => setCheckKeys(e.target.checked)} />
            {t('preview.checkKeys')}
          </label>
          <label className="flex items-center gap-1.5" title={t('key.includeUnmappedHint')}>
            <input
              type="checkbox"
              checked={!spec.omitUnmapped}
              onChange={(e) => {
                // A mapping setting (the migration writes the same documents), so it goes on the spec;
                // the preview re-runs with just this change.
                const omitUnmapped = !e.target.checked
                apply((s) => ({ ...s, omitUnmapped }))
                setSnapshot((s) => ({ ...s, omitUnmapped }))
              }}
            />
            {t('preview.includeUnmapped')}
          </label>
          <Button size="sm" variant="secondary" onClick={refresh} loading={q.isFetching}>
            <RefreshCw aria-hidden />
            {t('preview.refresh')}
          </Button>
          {sum ? (
            <span className="ml-auto flex flex-wrap gap-1.5">
              <Badge tone="success">{t('preview.valid', { count: sum.ok })}</Badge>
              {sum.errors ? <Badge tone="danger">{t('preview.errors', { count: sum.errors })}</Badge> : null}
              {sum.collisions ? <Badge tone="warning">{t('preview.collisions', { count: sum.collisions })}</Badge> : null}
              <Badge>{t('preview.avg', { size: formatBytes(sum.avgSizeBytes) })}</Badge>
              <Badge>{t('preview.max', { size: formatBytes(sum.maxSizeBytes) })}</Badge>
            </span>
          ) : null}
        </div>
        {q.error ? (
          <p role="alert" className="text-[13px] text-danger">
            {(q.error as Error).message}
          </p>
        ) : null}
        <div className="flex min-h-0 flex-1 gap-4">
          <ul aria-label={t('preview.rows')} className="w-64 shrink-0 overflow-y-auto border-r border-border pr-2">
            {items.map((it, i) => (
              <li key={i}>
                <button
                  type="button"
                  aria-current={it === item || undefined}
                  onClick={() => setIndex(i)}
                  className={cn('flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-xs hover:bg-surface-muted', it === item && 'bg-primary-soft')}
                >
                  {it.error ? <XCircle className="size-3.5 shrink-0 text-danger" aria-label={t('preview.error')} /> : <CheckCircle2 className="size-3.5 shrink-0 text-success" aria-label={t('preview.ok')} />}
                  <span className="min-w-0 truncate font-mono">{it.key ?? t('preview.noKey')}</span>
                  <span className="ml-auto shrink-0 text-subtle">{formatBytes(it.sizeBytes)}</span>
                </button>
              </li>
            ))}
          </ul>
          {item ? (
            <ItemDetail
              item={item}
              writeMode={writeMode}
              onJump={(path) => {
                const sel = selectionForPath(spec, path)
                if (sel) {
                  select(sel)
                  onOpenChange(false)
                }
              }}
            />
          ) : q.isFetching ? (
            <p className="text-[13px] text-muted">{t('common.loading')}</p>
          ) : null}
        </div>
      </DialogContent>
    </Dialog>
  )
}
