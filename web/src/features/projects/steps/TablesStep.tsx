import { ArrowRight, CheckCircle2, DatabaseZap, KeyRound, Lock, Search } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useNavigate, useOutletContext, useSearchParams } from 'react-router'
import { toast } from 'sonner'
import { AsyncBoundary } from '@/components/AsyncBoundary'
import { EmptyState } from '@/components/EmptyState'
import { ErrorState } from '@/components/ErrorState'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { hasRole } from '@/features/auth/Can'
import { useSession } from '@/features/auth/api'
import { useSchema, type Snapshot } from '@/features/discovery/api'
import { useBulkCreateMappings, useMappings } from '@/features/mapping/api'
import { folderName, sanitizeCollectionName } from '@/features/mapping/names'
import type { MappingSummary } from '@/features/mapping/types'
import { useScopes } from '@/features/targets/api'
import { emptyKeyspace, KeyspacePicker, type KeyspaceValue } from '@/features/targets/KeyspacePicker'
import { formatLocation } from '@/features/targets/kinds'
import { validFolderName, validScopeName } from '@/features/targets/names'
import { humanizeCount } from '@/lib/format'
import type { Project } from '../api'

type Row = { schema: string; table: string; key: string }
const keyOf = (schema: string, table: string) => `${schema}.${table}`

/** Step 3 (OC-035): pick source tables and a target collection per table, then create their mappings. */
export function TablesStep() {
  const { t } = useTranslation()
  const project = useOutletContext<Project>()
  const schema = useSchema(project.source?.id ?? '')
  const mappings = useMappings(project.id)
  if (schema.data === null) {
    return (
      <div className="px-8 py-7">
        <EmptyState
          icon={<DatabaseZap />}
          title={t('tables.discoverFirst')}
          body={t('tables.discoverFirstBody')}
          action={
            <Button asChild>
              <Link to={`/projects/${project.id}/source/schema`}>{t('explorer.explore')}</Link>
            </Button>
          }
        />
      </div>
    )
  }
  return (
    <div className="px-8 py-7">
      <AsyncBoundary query={schema}>
        {(snap) => <AsyncBoundary query={mappings}>{(ms) => <TablesPicker project={project} snapshot={snap!} existing={ms} />}</AsyncBoundary>}
      </AsyncBoundary>
    </div>
  )
}

function TablesPicker({ project, snapshot, existing }: { project: Project; snapshot: Snapshot; existing: MappingSummary[] }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const session = useSession()
  const canEdit = hasRole(session.data?.role, 'editor')
  const mapped = useMemo(() => new Map(existing.map((m) => [keyOf(m.sourceSchema, m.sourceTable), m])), [existing])
  // A composed mapping is listed by the table that drives it (OC-047).
  const drivenBy = useMemo(() => new Map(existing.filter((m) => m.mode === 'composed').map((m) => [keyOf(m.driving.schema, m.driving.name), m])), [existing])
  const kind = project.target?.type ?? 'couchbase'
  const isDir = kind === 'directory'
  const first = existing[0]?.target
  const [target, setTarget] = useState<KeyspaceValue>(first ? { bucket: first.bucket, scope: first.scope, collection: '' } : emptyKeyspace)
  const preselect = params.get('select')
  const [selected, setSelected] = useState<Set<string>>(() => new Set(preselect && !mapped.has(preselect) ? [preselect] : []))
  const [names, setNames] = useState<Record<string, string>>({})
  const [deferred, setDeferred] = useState<Record<string, boolean>>({})
  const [query, setQuery] = useState('')
  const bulk = useBulkCreateMappings(project.id)
  const scopes = useScopes(isDir ? '' : (project.target?.id ?? ''), target.bucket)
  const existingCollections = useMemo(
    () => new Set(scopes.data?.find((s) => s.name === target.scope)?.collections.map((c) => c.name) ?? []),
    [scopes.data, target.scope],
  )

  const rows: Row[] = useMemo(
    () =>
      snapshot.schemas
        .flatMap((s) => s.tables.filter((tb) => tb.readable).map((tb) => ({ schema: s.name, table: tb.name, key: keyOf(s.name, tb.name) })))
        .filter((r) => r.key.toLowerCase().includes(query.trim().toLowerCase())),
    [snapshot, query],
  )
  const tableOf = (r: Row) => snapshot.schemas.find((s) => s.name === r.schema)!.tables.find((x) => x.name === r.table)!
  const collectionFor = (r: Row) => names[r.key] ?? (isDir ? folderName : sanitizeCollectionName)(r.table)
  const willCreate = (r: Row) => !isDir && !!target.scope && !existingCollections.has(collectionFor(r))
  const validName = isDir ? validFolderName : validScopeName
  const chosen = rows.filter((r) => selected.has(r.key) && !mapped.has(r.key))
  const invalid = chosen.filter((r) => !validName(collectionFor(r)))
  const ready = isDir || (!!target.bucket && !!target.scope)

  const toggle = (key: string) =>
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })

  const create = () =>
    bulk.mutate(
      chosen.map((r) => ({
        sourceSchema: r.schema,
        sourceTable: r.table,
        // Couchbase keeps the 1.0 target form; other kinds send a 2.0 location (OC-036).
        ...(isDir
          ? { location: { kind: 'directory' as const, folder: collectionFor(r), createIfMissing: true } }
          : { target: { bucket: target.bucket, scope: target.scope, collection: collectionFor(r), createIfMissing: willCreate(r) && (deferred[r.key] ?? true) } }),
      })),
      {
        onSuccess: (results) => {
          const ok = results.filter((r) => r.mapping).length
          const failed = results.filter((r) => r.error)
          if (ok) toast.success(t('tables.created', { count: ok }))
          failed.forEach((f) => toast.error(`${f.sourceSchema}.${f.sourceTable}: ${f.error?.detail ?? f.error?.title}`))
          setSelected(new Set())
          if (ok && !failed.length) void navigate(`/projects/${project.id}/mappings`)
        },
      },
    )

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-col gap-1.5">
        <h2 className="text-lg font-semibold">{t('tables.title')}</h2>
        <p className="max-w-3xl text-[13px] text-muted">{t('tables.intro')}</p>
      </div>
      <section aria-label={t('tables.targetDefaults')} className="max-w-3xl rounded-[var(--radius-card)] border border-border bg-surface p-4">
        {isDir ? (
          <p className="text-[13px] text-muted">{t('tables.directoryIntro')}</p>
        ) : (
          <KeyspacePicker connId={project.target?.id ?? ''} value={target} onChange={setTarget} scopeOnly />
        )}
      </section>
      <div className="relative max-w-sm">
        <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-subtle" aria-hidden />
        <Input value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t('tables.search')} aria-label={t('tables.search')} className="h-8 pl-8 text-[13px]" />
      </div>
      <div className="overflow-auto rounded-[var(--radius-card)] border border-border">
        <table className="w-full border-collapse text-[13px]">
          <thead className="bg-surface-muted text-left text-xs text-muted">
            <tr>
              <th scope="col" className="w-10 px-3 py-2">
                <span className="sr-only">{t('tables.select')}</span>
              </th>
              <th scope="col" className="px-3 py-2 font-medium">{t('tables.table')}</th>
              <th scope="col" className="px-3 py-2 font-medium">{t('tables.rows')}</th>
              <th scope="col" className="px-3 py-2 font-medium">{t(isDir ? 'tables.targetFolder' : 'tables.targetCollection')}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => {
              const tb = tableOf(r)
              const own = mapped.get(r.key)
              const m = drivenBy.get(r.key) ?? own
              const checked = !!own || selected.has(r.key)
              return (
                <tr key={r.key} className="border-t border-border">
                  <td className="px-3 py-2">
                    <input type="checkbox" aria-label={t('tables.selectTable', { table: r.key })} checked={checked} disabled={!!own || !canEdit} onChange={() => toggle(r.key)} />
                  </td>
                  <td className="px-3 py-2">
                    <span className="flex items-center gap-2">
                      <span className="text-muted">{r.schema}.</span>
                      <span className="font-medium">{r.table}</span>
                      {tb.primaryKey.length ? <KeyRound className="size-3.5 text-warning" aria-label={t('explorer.pk')} /> : <Badge tone="warning">{t('tables.noPk')}</Badge>}
                    </span>
                  </td>
                  <td className="px-3 py-2 font-mono text-xs text-muted">{tb.rowEstimate != null ? `${tb.rowCountExact ? '' : '~'}${humanizeCount(tb.rowEstimate)}` : '—'}</td>
                  <td className="px-3 py-2">
                    {m ? (
                      <Link to={`/projects/${project.id}/mappings/${m.id}`} className="flex items-center gap-1.5 text-primary-soft-foreground hover:underline">
                        <CheckCircle2 className="size-3.5 text-success" aria-hidden />
                        {t('tables.mappedTo', { keyspace: formatLocation(m.location) })}
                        {m.mode === 'composed' ? <Badge tone="primary">{t('composer.badge')}</Badge> : null}
                      </Link>
                    ) : selected.has(r.key) ? (
                      <span className="flex flex-wrap items-center gap-2">
                        <Input
                          aria-label={t(isDir ? 'tables.folderFor' : 'tables.collectionFor', { table: r.key })}
                          value={collectionFor(r)}
                          onChange={(e) => { const v = e.target.value; setNames((n) => ({ ...n, [r.key]: v })) }}
                          invalid={!validName(collectionFor(r))}
                          className="h-8 w-56 font-mono text-xs"
                        />
                        {willCreate(r) ? (
                          <label className="flex items-center gap-1.5 text-xs text-muted">
                            <input type="checkbox" checked={deferred[r.key] ?? true} onChange={(e) => { const v = e.target.checked; setDeferred((d) => ({ ...d, [r.key]: v })) }} />
                            {t('tables.createLater')}
                          </label>
                        ) : null}
                      </span>
                    ) : null}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
        {snapshot.schemas.some((s) => s.tables.some((tb) => !tb.readable)) ? (
          <p className="flex items-center gap-1.5 border-t border-border px-3 py-2 text-xs text-subtle">
            <Lock className="size-3" aria-hidden />
            {t('tables.unreadableHidden')}
          </p>
        ) : null}
      </div>
      {bulk.error ? <ErrorState error={bulk.error} /> : null}
      <footer className="sticky bottom-0 flex h-16 items-center justify-between gap-3 border-t border-border bg-surface px-1">
        <span className="text-[13px] text-muted">{t('tables.selectedCount', { count: chosen.length })}</span>
        <div className="flex gap-2">
          {existing.length ? (
            <Button variant="secondary" asChild>
              <Link to={`/projects/${project.id}/mappings`}>
                {t('tables.openMappings')}
                <ArrowRight aria-hidden />
              </Link>
            </Button>
          ) : null}
          {canEdit ? (
            <Button disabled={!chosen.length || !ready || invalid.length > 0} loading={bulk.isPending} onClick={create}>
              {t('tables.create', { count: chosen.length })}
            </Button>
          ) : null}
        </div>
      </footer>
    </div>
  )
}
