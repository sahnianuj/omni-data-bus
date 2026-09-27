import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useSourceTable } from '@/features/discovery/api'
import { rename } from '@/lib/naming'
import { addEmbed, type AddEmbedRequest } from './api'
import { DEFAULT_EMBED_LIMIT } from './RelatedTables'
import { useWorkbench, useWorkbenchStore } from './store'
import type { EmbedCandidate } from './types'

const selectCls = 'h-8 w-full rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 text-[13px]'

/**
 * The Embed dialog (OC-021 AC2): the shape comes from the foreign key's direction; the user names
 * the field, orders and limits an array, and can generate the item fields and auto-map them.
 */
export function EmbedDialog({ candidate, onClose }: { candidate: EmbedCandidate; onClose: () => void }) {
  const { t } = useTranslation()
  const store = useWorkbenchStore()
  const view = useWorkbench((s) => s.view)
  const replace = useWorkbench((s) => s.replace)
  const many = candidate.kind === 'one-to-many'
  const table = useSourceTable(`${view.projectId}::source`, candidate.table.schema, candidate.table.name)
  const [name, setName] = useState(() => rename(candidate.table.name))
  const [orderBy, setOrderBy] = useState('')
  const [desc, setDesc] = useState(false)
  const [limit, setLimit] = useState(DEFAULT_EMBED_LIMIT)
  const [empty, setEmpty] = useState<'array' | 'omit'>('array')
  const [generate, setGenerate] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const limitId = useId()

  const submit = async () => {
    setBusy(true)
    setError(null)
    try {
      await store.getState().flush()
      const body: AddEmbedRequest = { kind: candidate.kind, table: candidate.table, join: candidate.join, targetName: name.trim(), generate }
      if (many) {
        body.limit = limit
        body.empty = empty
        if (orderBy) body.orderBy = [{ column: orderBy, desc }]
      }
      replace(await addEmbed(view.projectId, view.id, store.getState().version, body))
      onClose()
    } catch (e) {
      setError(e)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('embed.title', { table: candidate.table.name })}</DialogTitle>
          <DialogDescription>{t('embed.description', { table: `${candidate.table.schema}.${candidate.table.name}` })}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-3 text-[13px]">
          <div className="flex items-center justify-between">
            <span className="text-muted">{t('embed.kind')}</span>
            <span>{t(many ? 'embed.kindMany' : 'embed.kindOne')}</span>
          </div>
          <label className="grid gap-1">
            <span className="text-muted">{t('embed.fieldName')}</span>
            <Input value={name} onChange={(e) => setName(e.target.value)} />
          </label>
          {many ? (
            <>
              <div className="grid grid-cols-[1fr_auto] items-end gap-2">
                <label className="grid gap-1">
                  <span className="text-muted">{t('embed.orderBy')}</span>
                  <select className={selectCls} value={orderBy} onChange={(e) => setOrderBy(e.target.value)}>
                    <option value="">{t('embed.orderNone')}</option>
                    {(table.data?.columns ?? []).map((c) => (
                      <option key={c.name} value={c.name}>
                        {c.name}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="flex h-8 items-center gap-1.5">
                  <input type="checkbox" checked={desc} disabled={!orderBy} onChange={(e) => setDesc(e.target.checked)} />
                  {t('embed.desc')}
                </label>
              </div>
              <div className="grid gap-1">
                <label htmlFor={limitId} className="text-muted">
                  {t('embed.limit')}
                </label>
                <span className="flex items-center gap-2">
                  <Input
                    id={limitId}
                    type="number"
                    min={1}
                    max={100000}
                    className="w-28"
                    aria-describedby={`${limitId}-hint`}
                    value={limit}
                    onChange={(e) => setLimit(Number(e.target.value))}
                  />
                  <span id={`${limitId}-hint`} className="text-subtle">
                    {t('embed.limitHint')}
                  </span>
                </span>
              </div>
              <label className="grid gap-1">
                <span className="text-muted">{t('embed.empty')}</span>
                <select className={selectCls} value={empty} onChange={(e) => setEmpty(e.target.value as 'array' | 'omit')}>
                  <option value="array">{t('embed.emptyArray')}</option>
                  <option value="omit">{t('embed.emptyOmit')}</option>
                </select>
              </label>
            </>
          ) : null}
          <label className="flex items-start gap-2">
            <input type="checkbox" className="mt-0.5" checked={generate} onChange={(e) => setGenerate(e.target.checked)} />
            <span>
              {t('embed.generate')}
              <span className="block text-xs text-subtle">{t(candidate.kind === 'many-to-one' ? 'embed.generateHintObject' : 'embed.generateHint')}</span>
            </span>
          </label>
        </div>
        {error ? <ErrorState error={error} /> : null}
        <DialogFooter>
          <Button variant="secondary" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button loading={busy} disabled={name.trim() === '' || (many && (limit < 1 || limit > 100000))} onClick={() => void submit()}>
            {t('embed.submit')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
