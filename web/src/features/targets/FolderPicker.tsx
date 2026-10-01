import { ArrowUp, ChevronRight, Folder, FolderOpen, FolderPlus, HardDrive } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AsyncBoundary } from '@/components/AsyncBoundary'
import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useFolders, useMakeFolder } from './kinds'

type Props = { open: boolean; onOpenChange: (open: boolean) => void; initialPath?: string; onPick: (path: string) => void }

const validName = (n: string) => /^[A-Za-z0-9_-][A-Za-z0-9 ._-]{0,63}$/.test(n.trim())

/**
 * Choose the folder a directory target writes to (OC-038): browse the server's allowed areas (its
 * user's home folder and /data), open folders, create one, and use the current folder.
 */
export function FolderPicker({ open, onOpenChange, initialPath = '', onPick }: Props) {
  const { t } = useTranslation()
  const [path, setPath] = useState(initialPath)
  const [newName, setNewName] = useState<string | null>(null)
  const listing = useFolders(path, open)
  const make = useMakeFolder()
  const create = () => {
    if (newName === null || !validName(newName)) return
    make.mutate({ parent: path, name: newName.trim() }, { onSuccess: (r) => { setNewName(null); setPath(r.path) } })
  }
  const crumbs = path ? path.split('/').filter(Boolean) : []
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{t('dir.picker.title')}</DialogTitle>
          <DialogDescription>{t('dir.picker.description')}</DialogDescription>
        </DialogHeader>
        <div className="flex items-center gap-2">
          <Button variant="ghost" size="icon" aria-label={t('dir.picker.up')} disabled={!path} onClick={() => setPath(listing.data?.parent ?? '')}>
            <ArrowUp aria-hidden />
          </Button>
          <nav aria-label={t('dir.picker.location')} className="flex min-w-0 flex-1 flex-wrap items-center gap-0.5 font-mono text-xs">
            <button type="button" className="rounded px-1 text-primary hover:underline" onClick={() => setPath('')}>
              {t('dir.picker.areas')}
            </button>
            {crumbs.map((c, i) => (
              <span key={i} className="flex items-center gap-0.5">
                <ChevronRight className="size-3 text-subtle" aria-hidden />
                <button type="button" className="rounded px-1 text-primary hover:underline" onClick={() => setPath('/' + crumbs.slice(0, i + 1).join('/'))}>
                  {c}
                </button>
              </span>
            ))}
          </nav>
        </div>
        <div className="h-72 overflow-y-auto rounded-[var(--radius-card)] border border-border">
          <AsyncBoundary query={listing}>
            {(l) =>
              l.folders.length === 0 ? (
                <p className="p-4 text-[13px] text-muted">{t('dir.picker.empty')}</p>
              ) : (
                <ul aria-label={t('dir.picker.folders')} className="divide-y divide-border">
                  {l.folders.map((f) => (
                    <li key={f.path}>
                      <button type="button" onClick={() => setPath(f.path)} className="flex w-full items-center gap-2.5 px-3 py-2 text-left text-sm hover:bg-surface-muted">
                        {l.path ? <Folder className="size-4 shrink-0 text-muted" aria-hidden /> : <HardDrive className="size-4 shrink-0 text-muted" aria-hidden />}
                        <span className="flex-1 truncate">{f.name}</span>
                        <ChevronRight className="size-3.5 text-subtle" aria-hidden />
                      </button>
                    </li>
                  ))}
                </ul>
              )
            }
          </AsyncBoundary>
        </div>
        {listing.data?.canCreate ? (
          newName === null ? (
            <Button variant="link" size="sm" className="self-start px-0" onClick={() => setNewName('')}>
              <FolderPlus aria-hidden />
              {t('dir.picker.newFolder')}
            </Button>
          ) : (
            <form className="flex items-end gap-2" onSubmit={(e) => { e.preventDefault(); create() }}>
              <label className="flex flex-1 flex-col gap-1 text-xs text-muted">
                {t('dir.picker.newFolderName')}
                <Input autoFocus value={newName} onChange={(e) => setNewName(e.target.value)} invalid={newName !== '' && !validName(newName)} className="h-8 text-sm" />
              </label>
              <Button type="button" size="sm" variant="ghost" onClick={() => setNewName(null)}>
                {t('common.cancel')}
              </Button>
              <Button type="submit" size="sm" variant="secondary" disabled={!validName(newName)} loading={make.isPending}>
                {t('dir.picker.create')}
              </Button>
            </form>
          )
        ) : null}
        {make.error ? <ErrorState error={make.error} /> : null}
        <DialogFooter className="items-center sm:justify-between">
          <span className="flex min-w-0 items-center gap-1.5 font-mono text-xs text-muted">
            <FolderOpen className="size-3.5 shrink-0" aria-hidden />
            <span className="truncate">{path || t('dir.picker.noneYet')}</span>
          </span>
          <Button disabled={!path} onClick={() => { onPick(path); onOpenChange(false) }}>
            {t('dir.picker.use')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
