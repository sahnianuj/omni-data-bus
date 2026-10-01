import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useMappings } from '@/features/mapping/api'
import { folderName, sanitizeCollectionName } from '@/features/mapping/names'
import { formatLocation, type Location } from '@/features/targets/kinds'

export type Relocate = { bucket?: string; scope?: string }

type Props = {
  projectId: string
  /** The target kind being saved. */
  to: string
  open: boolean
  saving?: boolean
  /** A failed move-and-save. */
  error?: Error | null
  onOpenChange: (open: boolean) => void
  onConfirm: (relocate: Relocate) => void
}

/** Where a mapping moves on a target of kind `to`; mirrors connections.relocated (OC-068). */
export function relocated(at: Location, table: string, to: string, rel: Relocate): Location {
  let name = at.kind === 'directory' ? ((at.folder ?? '').replace(/^\/+|\/+$/g, '').split('/').pop() ?? '') : (at.collection ?? '')
  if (!name) name = table
  if (to === 'directory') return { kind: 'directory', folder: folderName(name), createIfMissing: true }
  return { kind: 'couchbase', bucket: rel.bucket?.trim(), scope: rel.scope?.trim(), collection: sanitizeCollectionName(name), createIfMissing: true }
}

/**
 * Shown when a target kind change is refused because mappings have locations of the old kind
 * (TARGET_KIND_IN_USE): it lists where each mapping moves, and for Couchbase asks for the bucket
 * and scope, then re-saves with `relocate` (OC-068).
 */
export function RelocateDialog({ projectId, to, open, saving, error, onOpenChange, onConfirm }: Props) {
  const { t } = useTranslation()
  const mappings = useMappings(projectId)
  const [bucket, setBucket] = useState('')
  const [scope, setScope] = useState('_default')
  const toCouchbase = to === 'couchbase'
  const rel: Relocate = toCouchbase ? { bucket, scope } : {}
  const moving = (mappings.data ?? []).filter((m) => m.location && m.location.kind !== to)
  const ready = !toCouchbase || (bucket.trim() !== '' && scope.trim() !== '')
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('repo.relocateTitle')}</DialogTitle>
          <DialogDescription>{t(toCouchbase ? 'repo.relocateBodyCouchbase' : 'repo.relocateBodyDirectory')}</DialogDescription>
        </DialogHeader>
        {toCouchbase ? (
          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="relocate-bucket">{t('repo.relocateBucket')}</Label>
              <Input id="relocate-bucket" value={bucket} onChange={(e) => setBucket(e.target.value)} autoComplete="off" />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="relocate-scope">{t('repo.relocateScope')}</Label>
              <Input id="relocate-scope" value={scope} onChange={(e) => setScope(e.target.value)} autoComplete="off" />
            </div>
          </div>
        ) : null}
        <ul aria-label={t('repo.relocateList')} className="max-h-60 overflow-auto rounded-md border border-border text-[13px]">
          {moving.map((m) => (
            <li key={m.id} className="flex flex-wrap items-baseline gap-x-2 border-b border-border px-3 py-1.5 last:border-b-0">
              <span className="font-medium">{m.sourceSchema}.{m.sourceTable}</span>
              <span className="font-mono text-xs text-muted">
                {formatLocation(m.location)} → {formatLocation(relocated(m.location!, m.sourceTable, to, rel))}
              </span>
            </li>
          ))}
        </ul>
        {error ? <ErrorState error={error} /> : null}
        <DialogFooter>
          <Button variant="secondary" onClick={() => onOpenChange(false)}>
            {t('common.cancel')}
          </Button>
          <Button disabled={!ready} loading={saving} onClick={() => onConfirm(rel)}>
            {t('repo.relocateConfirm')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
