import { ChevronRight, Folder, FolderPlus } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { AsyncBoundary } from '@/components/AsyncBoundary'
import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { humanizeCount } from '@/lib/format'
import { useCreateLocation, useLocations } from './kinds'
import { validFolderName } from './names'

/** Browse and create sub-folders of a directory target (OC-038 AC3): the directory equivalent of the keyspace browser. */
export function DirectoryBrowser({ connId, root }: { connId: string; root: string }) {
  const { t } = useTranslation()
  const [parent, setParent] = useState('')
  const [name, setName] = useState('')
  const folders = useLocations(connId, parent)
  const create = useCreateLocation(connId)
  const segs = parent ? parent.split('/') : []
  const next = parent ? `${parent}/${name.trim()}` : name.trim()
  const valid = name.trim() !== '' && validFolderName(next)
  const submit = () =>
    create.mutate(
      { kind: 'directory', folder: next },
      { onSuccess: () => { toast.success(t('dir.browser.created', { folder: next })); setName('') } },
    )
  return (
    <div className="flex flex-col gap-4">
      <nav aria-label={t('dir.browser.path')} className="flex flex-wrap items-center gap-1 font-mono text-[13px]">
        <button type="button" className="text-primary hover:underline" onClick={() => setParent('')}>
          {root}
        </button>
        {segs.map((s, i) => (
          <span key={i} className="flex items-center gap-1">
            <ChevronRight className="size-3 text-subtle" aria-hidden />
            <button type="button" className="text-primary hover:underline" onClick={() => setParent(segs.slice(0, i + 1).join('/'))}>
              {s}
            </button>
          </span>
        ))}
      </nav>
      <AsyncBoundary query={folders}>
        {(nodes) =>
          nodes.length === 0 ? (
            <p className="text-[13px] text-muted">{t('dir.browser.empty')}</p>
          ) : (
            <ul aria-label={t('dir.browser.folders')} className="flex flex-col divide-y divide-border rounded-[var(--radius-card)] border border-border">
              {nodes.map((n) => (
                <li key={n.path}>
                  <button type="button" onClick={() => setParent(n.path)} className="flex w-full items-center gap-2.5 px-3 py-2 text-left text-sm hover:bg-surface-muted">
                    <Folder className="size-4 text-muted" aria-hidden />
                    <span className="flex-1 font-mono">{n.name}/</span>
                    <span className="text-xs text-muted">
                      {t('dir.browser.files', { count: n.docCount ?? 0, shown: `${humanizeCount(n.docCount ?? 0)}${n.docCountMore ? '+' : ''}` })}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )
        }
      </AsyncBoundary>
      {segs.length < 3 ? (
        <form className="flex items-end gap-2" onSubmit={(e) => { e.preventDefault(); if (valid) submit() }}>
          <label className="flex flex-1 flex-col gap-1 text-xs text-muted">
            {t('dir.browser.newFolder')}
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="orders" invalid={name !== '' && !valid} className="h-8 font-mono text-xs" />
          </label>
          <Button type="submit" size="sm" variant="secondary" disabled={!valid} loading={create.isPending}>
            <FolderPlus aria-hidden />
            {t('dir.browser.create')}
          </Button>
        </form>
      ) : null}
      {create.error ? <ErrorState error={create.error} /> : null}
    </div>
  )
}
