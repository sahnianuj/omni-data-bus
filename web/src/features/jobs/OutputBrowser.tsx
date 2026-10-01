import { Copy, Download, FileJson, Search } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { AsyncBoundary } from '@/components/AsyncBoundary'
import { EmptyState } from '@/components/EmptyState'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useMapping } from '@/features/mapping/api'
import { formatLocation } from '@/features/targets/kinds'
import { formatBytes } from '@/lib/format'
import { documentUrl, useJobOutput, useStoredDocument, type Job } from './api'

type MappingRef = { mappingId: string; table: string }

const HOLE = /\{([^{}$][^{}]*)\}/g

/** The key template's columns ("{order_id}::{line_no}" → order_id, line_no); none for {$uuid} keys. */
export function keyColumns(template: string): string[] {
  if (template.includes('{$')) return []
  return [...template.matchAll(HOLE)].map((m) => m[1]!.split(':')[0]!)
}

/** Fills a key template with values in order (mirrors the server's RenderKey). */
export function renderKey(template: string, values: string[]): string {
  let i = 0
  return template.replace(HOLE, () => values[i++] ?? '')
}

/**
 * A finished job's Output tab (OC-041): find a document by its primary key (the key template's
 * columns), see where it is and what it holds, download it; for a directory, browse every file.
 */
export function OutputBrowser({ job, mappings }: { job: Job; mappings: MappingRef[] }) {
  const { t } = useTranslation()
  const [mid, setMid] = useState(mappings[0]?.mappingId ?? '')
  return (
    <div className="flex flex-col gap-4">
      {mappings.length > 1 ? (
        <label className="flex items-center gap-2 text-[13px]">
          {t('output.table')}
          <select value={mid} onChange={(e) => setMid(e.target.value)} className="h-8 rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 text-sm">
            {mappings.map((m) => (
              <option key={m.mappingId} value={m.mappingId}>
                {m.table}
              </option>
            ))}
          </select>
        </label>
      ) : null}
      {mid ? <MappingOutput key={mid} job={job} mappingId={mid} /> : null}
    </div>
  )
}

function MappingOutput({ job, mappingId }: { job: Job; mappingId: string }) {
  const mapping = useMapping(job.projectId, mappingId)
  return <AsyncBoundary query={mapping}>{(m) => <Finder job={job} mappingId={mappingId} template={m.spec.key.template} location={m.location} />}</AsyncBoundary>
}

function Finder({ job, mappingId, template, location }: { job: Job; mappingId: string; template: string; location: Parameters<typeof formatLocation>[0] }) {
  const { t } = useTranslation()
  const cols = useMemo(() => keyColumns(template), [template])
  const [values, setValues] = useState<string[]>(() => cols.map(() => ''))
  const [raw, setRaw] = useState('')
  const [key, setKey] = useState('')
  const [cursor, setCursor] = useState('')
  const isDir = location?.kind === 'directory'
  const doc = useStoredDocument(job.projectId, mappingId, key)
  const listing = useJobOutput(job.id, mappingId, cursor, isDir)
  const typed = cols.length > 0 ? renderKey(template, values) : raw.trim()
  const complete = cols.length > 0 ? values.every((v) => v.trim() !== '') : raw.trim() !== ''
  return (
    <div className="flex flex-col gap-4">
      <p className="text-[13px] text-muted">{t(isDir ? 'output.introDir' : 'output.intro', { location: formatLocation(location) })}</p>
      <form
        className="flex flex-wrap items-end gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          if (complete) setKey(typed)
        }}
      >
        {cols.length > 0 ? (
          cols.map((c, i) => (
            <label key={c} className="flex flex-col gap-1 text-xs text-muted">
              {c}
              <Input value={values[i] ?? ''} onChange={(e) => setValues((v) => v.map((x, j) => (j === i ? e.target.value : x)))} className="h-8 w-40 font-mono text-xs" />
            </label>
          ))
        ) : (
          <label className="flex flex-col gap-1 text-xs text-muted">
            {t('output.rawKey')}
            <Input value={raw} onChange={(e) => setRaw(e.target.value)} className="h-8 w-72 font-mono text-xs" />
          </label>
        )}
        <Button type="submit" size="sm" disabled={!complete}>
          <Search aria-hidden />
          {t('output.find')}
        </Button>
      </form>
      {key ? (
        <AsyncBoundary query={doc}>
          {(d) =>
            d === null ? (
              <EmptyState icon={<FileJson />} title={t('output.notFound', { key })} body={t('output.notFoundBody')} />
            ) : (
              <article aria-label={t('output.document')} className="flex flex-col gap-2 rounded-[var(--radius-card)] border border-border bg-surface p-4">
                <div className="flex flex-wrap items-center gap-2 text-[13px]">
                  <span className="font-mono font-medium">{d.path ?? d.key}</span>
                  {d.path ? (
                    <Button variant="ghost" size="icon" aria-label={t('output.copyPath')} onClick={() => void navigator.clipboard?.writeText(d.path!).then(() => toast.success(t('output.copied')))}>
                      <Copy aria-hidden />
                    </Button>
                  ) : null}
                  <span className="text-muted">{formatBytes(d.bytes)}</span>
                  {d.modifiedAt ? <span className="text-muted">· {new Date(d.modifiedAt).toLocaleString()}</span> : null}
                  <Button variant="secondary" size="sm" className="ml-auto" asChild>
                    <a href={documentUrl(job.projectId, mappingId, d.key, true)} download>
                      <Download aria-hidden />
                      {t('output.download')}
                    </a>
                  </Button>
                </div>
                <pre className="max-h-96 overflow-auto rounded-md bg-surface-muted p-3 font-mono text-xs">{JSON.stringify(d.document, null, 2)}</pre>
              </article>
            )
          }
        </AsyncBoundary>
      ) : null}
      {isDir ? (
        <section aria-label={t('output.files')} className="flex flex-col gap-2">
          <h4 className="text-xs font-semibold uppercase tracking-wide text-muted">{t('output.files')}</h4>
          <AsyncBoundary query={listing}>
            {(page) => (
              <>
                <ul className="divide-y divide-border rounded-[var(--radius-card)] border border-border text-[13px]">
                  {page.items.map((it) => (
                    <li key={it.key}>
                      <button type="button" className="flex w-full items-center gap-3 px-3 py-1.5 text-left hover:bg-surface-muted" onClick={() => setKey(it.key)}>
                        <span className="flex-1 truncate font-mono text-xs">{it.path}</span>
                        <span className="text-xs text-muted">{formatBytes(it.bytes)}</span>
                      </button>
                    </li>
                  ))}
                </ul>
                <div className="flex gap-2">
                  {cursor ? (
                    <Button variant="ghost" size="sm" onClick={() => setCursor('')}>
                      {t('output.first')}
                    </Button>
                  ) : null}
                  {page.nextCursor ? (
                    <Button variant="secondary" size="sm" onClick={() => setCursor(page.nextCursor)}>
                      {t('output.next')}
                    </Button>
                  ) : null}
                </div>
              </>
            )}
          </AsyncBoundary>
        </section>
      ) : null}
    </div>
  )
}
