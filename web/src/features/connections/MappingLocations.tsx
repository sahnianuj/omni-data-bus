import { AlertTriangle, Pencil } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { toast } from 'sonner'
import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useMoveMappings, useMappings } from '@/features/mapping/api'
import { KeyspacePicker, type KeyspaceValue } from '@/features/targets/KeyspacePicker'

type Group = { bucket: string; scope: string; mappings: { id: string; table: string; collection: string }[] }

/**
 * Where the project's mappings write on a Couchbase target, by bucket and scope: shown with the
 * target's connection so a change of cluster or bucket can be checked and followed. Each group
 * can be moved to another bucket and scope (collection names are kept).
 */
export function MappingLocations({ projectId, connId, canEdit }: { projectId: string; connId: string; canEdit: boolean }) {
  const { t } = useTranslation()
  const mappings = useMappings(projectId)
  const [moving, setMoving] = useState<Group | null>(null)
  const groups = useMemo(() => {
    const out = new Map<string, Group>()
    for (const m of mappings.data ?? []) {
      const loc = m.location
      if (!loc || loc.kind !== 'couchbase') continue
      const bucket = loc.bucket ?? ''
      const scope = loc.scope ?? ''
      const key = `${bucket}.${scope}`
      if (!out.has(key)) out.set(key, { bucket, scope, mappings: [] })
      out.get(key)!.mappings.push({ id: m.id, table: `${m.driving.schema}.${m.driving.name}`, collection: loc.collection ?? '' })
    }
    return [...out.values()].sort((a, b) => b.mappings.length - a.mappings.length)
  }, [mappings.data])

  if (mappings.isError) return <ErrorState error={mappings.error} onRetry={() => void mappings.refetch()} />
  if (!groups.length) return null
  return (
    <section aria-label={t('repo.locations.title')} className="flex flex-col gap-3 rounded-[var(--radius-card)] border border-border bg-surface p-4">
      <div className="flex flex-col gap-1">
        <h3 className="text-sm font-semibold">{t('repo.locations.title')}</h3>
        <p className="text-xs text-muted">{t('repo.locations.intro')}</p>
      </div>
      {groups.length > 1 ? (
        <p role="status" className="flex items-start gap-2 rounded-md bg-warning-soft px-3 py-2 text-xs text-warning">
          <AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden />
          {t('repo.locations.mixed', { count: groups.length })}
        </p>
      ) : null}
      {groups.map((g) => (
        <div key={`${g.bucket}.${g.scope}`} className="flex flex-col gap-1.5 border-t border-border pt-3 first:border-t-0 first:pt-0">
          <div className="flex items-center justify-between gap-2">
            <span className="font-mono text-[13px]">
              <span className="text-subtle">{t('repo.locations.bucket')} </span>
              {g.bucket}
              <span className="text-subtle"> · {t('repo.locations.scope')} </span>
              {g.scope}
            </span>
            {canEdit ? (
              <Button variant="ghost" size="sm" onClick={() => setMoving(g)} aria-label={t('repo.locations.changeFor', { keyspace: `${g.bucket}.${g.scope}` })}>
                <Pencil aria-hidden />
                {t('repo.locations.change')}
              </Button>
            ) : null}
          </div>
          <ul className="flex flex-col gap-0.5 text-xs">
            {g.mappings.map((m) => (
              <li key={m.id} className="flex items-center justify-between gap-3">
                <Link to={`/projects/${projectId}/mappings/${m.id}`} className="truncate text-primary-soft-foreground hover:underline">{m.table}</Link>
                <span className="truncate font-mono text-muted">{m.collection}</span>
              </li>
            ))}
          </ul>
        </div>
      ))}
      {moving ? <MoveDialog projectId={projectId} connId={connId} group={moving} onClose={() => setMoving(null)} /> : null}
    </section>
  )
}

function MoveDialog({ projectId, connId, group, onClose }: { projectId: string; connId: string; group: Group; onClose: () => void }) {
  const { t } = useTranslation()
  const [to, setTo] = useState<KeyspaceValue>({ bucket: group.bucket, scope: group.scope, collection: '' })
  const move = useMoveMappings(projectId)
  const unchanged = to.bucket === group.bucket && to.scope === group.scope
  const confirm = () =>
    move.mutate(
      { ids: group.mappings.map((m) => m.id), bucket: to.bucket, scope: to.scope },
      {
        onSuccess: () => {
          toast.success(t('repo.locations.moved', { count: group.mappings.length, keyspace: `${to.bucket}.${to.scope}` }))
          onClose()
        },
      },
    )
  return (
    <Dialog open onOpenChange={(o) => (!o && !move.isPending ? onClose() : undefined)}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('repo.locations.moveTitle')}</DialogTitle>
          <DialogDescription>{t('repo.locations.moveBody', { count: group.mappings.length, keyspace: `${group.bucket}.${group.scope}` })}</DialogDescription>
        </DialogHeader>
        <KeyspacePicker connId={connId} value={to} onChange={setTo} scopeOnly />
        {move.error ? <ErrorState error={move.error} /> : null}
        <DialogFooter>
          <Button variant="secondary" onClick={onClose} disabled={move.isPending}>{t('common.cancel')}</Button>
          <Button onClick={confirm} loading={move.isPending} disabled={unchanged || !to.bucket || !to.scope}>
            {t('repo.locations.move', { count: group.mappings.length })}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
