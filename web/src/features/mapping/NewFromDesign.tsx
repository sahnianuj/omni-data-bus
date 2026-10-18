import { AlertTriangle, Upload } from 'lucide-react'
import { useId, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { HelpLink } from '@/components/HelpLink'
import { Input } from '@/components/ui/input'
import { hasRole } from '@/features/auth/Can'
import { useSession } from '@/features/auth/api'
import { sniffJson } from '@/features/docschema/sniffJson'
import type { Project } from '@/features/projects/api'
import { emptyKeyspace, KeyspacePicker, type KeyspaceValue } from '@/features/targets/KeyspacePicker'
import { formatLocation } from '@/features/targets/kinds'
import { ApiError } from '@/lib/api/client'
import { inferDocSchema, proposeDesign, saveMapping, useCreateMapping, type Proposal } from './api'
import { folderName, sanitizeCollectionName } from './names'
import type { DocSchema, MappingSummary } from './types'

/** Where a design's import waits for the composer of an existing mapping (OC-170). */
export const pendingImportKey = (mappingId: string) => `odb-import:${mappingId}`

type Ref = { schema: string; name: string }
const key = (t: Ref) => `${t.schema}.${t.name}`
/** The literal part of a key template before its first column ("cart::{id}" → "cart::"). */
const keyPrefix = (tpl: string) => tpl.split('{')[0] ?? ''

/**
 * New mapping from a design (OC-163 AC3): the customer's design, the driving table the
 * proposal picks from the connected source, and — when that table is already mapped — a
 * choice to update that mapping, create a separate one, or cancel (OC-170).
 */
export function NewFromDesign({ project, mappings, onClose }: { project: Project; mappings: MappingSummary[]; onClose: () => void }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const session = useSession()
  const canEdit = hasRole(session.data?.role, 'editor')
  const create = useCreateMapping(project.id)
  const fileRef = useRef<HTMLInputElement>(null)
  const textId = useId()
  const collId = useId()
  const [text, setText] = useState('')
  const [fileName, setFileName] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [design, setDesign] = useState<DocSchema | null>(null)
  const [proposal, setProposal] = useState<Proposal | null>(null)
  const [choice, setChoice] = useState(0)
  const [step, setStep] = useState<'input' | 'choose' | 'mapped'>('input')
  const isDir = project.target?.type === 'directory'
  const first = mappings[0]?.target
  const [target, setTarget] = useState<KeyspaceValue>(first ? { bucket: first.bucket, scope: first.scope, collection: '' } : emptyKeyspace)
  const [collection, setCollection] = useState('')
  const sniff = useMemo(() => (text.trim() ? sniffJson(text) : null), [text])
  const table = proposal?.driving[choice]?.table
  const coll = collection || (table ? (isDir ? folderName : sanitizeCollectionName)(table.name) : '')

  const fail = (e: unknown) => toast.error(e instanceof ApiError ? (e.detail ?? e.title) : String(e))
  const read = async () => {
    if (!sniff?.kind) return
    setBusy(true)
    try {
      const res = await inferDocSchema(sniff.kind, text, fileName)
      const p = await proposeDesign(project.id, res.docSchema)
      if (!p.driving.length) {
        toast.error(t('newFromDesign.noTable'))
        return
      }
      setDesign(res.docSchema)
      setProposal(p)
      setChoice(0)
      setStep('choose')
    } catch (e) {
      fail(e)
    } finally {
      setBusy(false)
    }
  }

  // The mappings already reading the chosen table: driven by it, or on it (OC-170).
  const existing = useMemo(() => (table ? mappings.filter((m) => key(m.driving) === key(table) || key({ schema: m.sourceSchema, name: m.sourceTable }) === key(table)) : []), [mappings, table])
  // A project maps each table once: a mapping on the table itself leaves only "update".
  const onTable = table ? mappings.find((m) => key({ schema: m.sourceSchema, name: m.sourceTable }) === key(table)) : undefined
  const location = isDir ? `${coll}/` : `${target.bucket}.${target.scope}.${coll}`
  const newKey = proposal?.spec.key.template ?? ''
  const overlaps = existing.filter((m) => formatLocation(m.location) === location && !!newKey && keyPrefix(m.keyTemplate ?? "") === keyPrefix(newKey))

  const next = () => (existing.length ? setStep('mapped') : void createNew())

  const createNew = async () => {
    if (!table || !design) return
    setBusy(true)
    try {
      const view = await create.mutateAsync({
        sourceSchema: table.schema,
        sourceTable: table.name,
        ...(isDir ? { location: { kind: 'directory' as const, folder: coll, createIfMissing: true } } : { target: { bucket: target.bucket, scope: target.scope, collection: coll, createIfMissing: true } }),
      })
      // The design, driven by the chosen table; the composer proposes the rest (OC-163).
      await saveMapping(project.id, view.id, view.version, { ...view.spec, mode: 'composed', root: { table }, docSchema: design, relations: [], fieldMappings: [] })
      onClose()
      void navigate(`/projects/${project.id}/mappings/${view.id}?propose=1`)
    } catch (e) {
      fail(e)
    } finally {
      setBusy(false)
    }
  }

  const update = (m: MappingSummary) => {
    try {
      window.sessionStorage.setItem(pendingImportKey(m.id), JSON.stringify({ text, fileName }))
    } catch {
      // No session storage: the composer opens the import empty.
    }
    onClose()
    void navigate(`/projects/${project.id}/mappings/${m.id}?import=pending`)
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <div className="flex items-center gap-1.5">
            <DialogTitle>{step === 'mapped' && table ? t('newFromDesign.mappedTitle', { table: key(table) }) : t('newFromDesign.title')}</DialogTitle>
            <HelpLink topic="designFirst" />
          </div>
          <DialogDescription>{step === 'mapped' ? t('newFromDesign.mappedBody') : t('newFromDesign.body')}</DialogDescription>
        </DialogHeader>

        {step === 'input' ? (
          <div className="flex flex-col gap-2">
            <label htmlFor={textId} className="sr-only">
              {t('newFromDesign.label')}
            </label>
            <textarea
              id={textId}
              rows={10}
              autoFocus
              spellCheck={false}
              value={text}
              onChange={(e) => {
                setText(e.target.value)
                setFileName(undefined)
              }}
              className="rounded-[var(--radius-control)] border border-border-strong bg-surface p-2 font-mono text-xs"
            />
            <div className="flex items-center gap-2 text-xs text-muted">
              <Button type="button" size="sm" variant="secondary" onClick={() => fileRef.current?.click()}>
                <Upload className="size-3.5" aria-hidden />
                {t('composer.import.choose')}
              </Button>
              <input
                ref={fileRef}
                type="file"
                accept=".json,.ndjson,.jsonl,application/json"
                aria-label={t('composer.import.fileLabel')}
                className="sr-only"
                tabIndex={-1}
                onChange={(e) => {
                  const f = e.target.files?.[0]
                  if (f)
                    void f.text().then((x) => {
                      setText(x)
                      setFileName(f.name)
                    })
                  e.target.value = ''
                }}
              />
              {fileName ? <span className="font-mono">{fileName}</span> : null}
            </div>
          </div>
        ) : null}

        {step === 'choose' && proposal ? (
          <div className="flex flex-col gap-3 text-[13px]">
            <fieldset className="flex flex-col gap-1.5">
              <legend className="mb-1 text-xs font-semibold uppercase tracking-wide text-muted">{t('newFromDesign.driving')}</legend>
              {proposal.driving.map((d, i) => (
                <label key={key(d.table)} className="flex items-start gap-2 rounded border border-border p-2">
                  <input type="radio" name="driving" className="mt-1" checked={choice === i} onChange={() => setChoice(i)} aria-label={key(d.table)} />
                  <span className="flex flex-col">
                    <span className="font-medium">
                      {key(d.table)} <span className="font-mono text-xs text-muted">{Math.round(d.score * 100)}%</span>
                    </span>
                    <span className="text-xs text-muted">{d.reasons.join(' · ')}</span>
                  </span>
                </label>
              ))}
            </fieldset>
            {!isDir ? <KeyspacePicker connId={project.target?.id ?? ''} value={target} onChange={setTarget} scopeOnly /> : null}
            <label htmlFor={collId} className="flex flex-col gap-1 text-xs font-medium text-muted">
              {t(isDir ? 'tables.targetFolder' : 'tables.targetCollection')}
              <Input id={collId} value={coll} onChange={(e) => setCollection(e.target.value)} className="h-8 text-[13px]" />
            </label>
          </div>
        ) : null}

        {step === 'mapped' ? (
          <div className="flex flex-col gap-3 text-[13px]">
            {existing.map((m) => (
              <article key={m.id} aria-label={t('newFromDesign.existing', { table: m.sourceTable })} className="flex flex-col gap-1 rounded-[var(--radius-card)] border border-border p-3">
                <div className="flex items-center gap-2">
                  <strong>{m.sourceTable}</strong>
                  <span className="text-xs text-muted">{t(`mappings.status.${m.status}`)}</span>
                  <span className="text-xs text-muted">{m.mode === 'composed' ? t('newFromDesign.composed') : t('newFromDesign.canvas')}</span>
                </div>
                <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-xs">
                  <dt className="text-muted">{t('newFromDesign.where')}</dt>
                  <dd className="font-mono">{formatLocation(m.location)}</dd>
                  <dt className="text-muted">{t('newFromDesign.key')}</dt>
                  <dd className="font-mono">{m.keyTemplate || '—'}</dd>
                  <dt className="text-muted">{t('newFromDesign.mappedFields')}</dt>
                  <dd>{t('newFromDesign.fields', { mapped: m.stats.mappedTargetLeaves, total: m.stats.totalTargetLeaves })}</dd>
                  <dt className="text-muted">{t('newFromDesign.edited')}</dt>
                  <dd>{t('newFromDesign.editedOn', { when: new Date(m.updatedAt).toLocaleString(), who: m.createdBy || '—' })}</dd>
                </dl>
                {canEdit ? (
                  <Button size="sm" className="self-start" onClick={() => update(m)}>
                    {t('newFromDesign.update')}
                  </Button>
                ) : (
                  <p className="text-xs text-muted">{t('newFromDesign.viewerCantUpdate')}</p>
                )}
              </article>
            ))}
            {onTable ? (
              <p className="text-xs text-muted">{t('newFromDesign.oneMappingPerTable', { table: onTable.sourceTable })}</p>
            ) : overlaps.length ? (
              <p role="alert" className="flex items-start gap-2 rounded bg-warning-soft p-2 text-xs text-warning">
                <AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden />
                {t('newFromDesign.overlap', { key: newKey, where: location })}
              </p>
            ) : null}
          </div>
        ) : null}

        <DialogFooter>
          <Button variant="secondary" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          {step === 'input' ? (
            <Button disabled={!sniff?.kind || busy} onClick={() => void read()}>
              {t('newFromDesign.read')}
            </Button>
          ) : null}
          {step === 'choose' ? (
            <Button disabled={busy || !coll || (!isDir && (!target.bucket || !target.scope))} onClick={next}>
              {t('newFromDesign.create')}
            </Button>
          ) : null}
          {step === 'mapped' ? (
            <>
              <Button variant="secondary" onClick={() => setStep('choose')}>
                {t('composer.import.back')}
              </Button>
              <Button variant="secondary" disabled={!canEdit || !!onTable || busy} onClick={() => void createNew()}>
                {t('newFromDesign.createAnyway')}
              </Button>
            </>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
