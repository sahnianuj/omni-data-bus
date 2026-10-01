import { FileJson, Upload } from 'lucide-react'
import { useId, useMemo, useRef, useState, type DragEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { sniffJson } from '@/features/docschema/sniffJson'
import { inferDocSchema } from '@/features/mapping/api'
import { useWorkbench } from '@/features/mapping/store'
import type { DocField, DocSchema } from '@/features/mapping/types'
import { ApiError } from '@/lib/api/client'
import { importDesign, nodeOf, OpError } from './composerOps'
import { ReimportStep } from './ReimportDialog'

type Inferred = { schema: DocSchema; warnings: string[] }

/** How many sample documents the text holds (one object, an array, or NDJSON). */
function sampleCount(text: string): number {
  try {
    const v: unknown = JSON.parse(text)
    return Array.isArray(v) ? v.length : 1
  } catch {
    return text.split('\n').filter((l) => l.trim()).length
  }
}

const optionalCount = (fs: DocField[] = []): number =>
  fs.reduce((n, f) => n + (f.required ? 0 : 1) + optionalCount(f.children) + optionalCount(f.items?.children), 0)

/**
 * Import the customer's JSON design (OC-059): paste it, choose a file or drop one, as sample
 * documents (one object, an array or NDJSON) or a JSON Schema. The server infers the design
 * (the same engine as the Design tab), then a report says what will change before it's applied.
 */
export function ImportDialog({ nodeId, initial, onClose }: { nodeId: string | null; initial?: { text: string; fileName?: string }; onClose: () => void }) {
  const { t } = useTranslation()
  const spec = useWorkbench((s) => s.history.present)
  const apply = useWorkbench((s) => s.apply)
  const [text, setText] = useState(initial?.text ?? '')
  const [fileName, setFileName] = useState(initial?.fileName)
  const [inferred, setInferred] = useState<Inferred | null>(null)
  const [busy, setBusy] = useState(false)
  const [dragging, setDragging] = useState(false)
  const [reconciling, setReconciling] = useState(false)
  const fileRef = useRef<HTMLInputElement>(null)
  const textId = useId()
  const replaceAll = nodeId === null
  const name = nodeId ? (nodeOf(spec, nodeId)?.name ?? '$') : '$'
  const sniff = useMemo(() => (text.trim() ? sniffJson(text) : null), [text])

  const load = async (file: File) => {
    setText(await file.text())
    setFileName(file.name)
    setInferred(null)
  }
  const onDrop = (e: DragEvent) => {
    const file = e.dataTransfer.files?.[0]
    setDragging(false)
    if (!file) return
    e.preventDefault()
    void load(file)
  }

  const read = async () => {
    if (!sniff?.kind) return
    setBusy(true)
    try {
      const res = await inferDocSchema(sniff.kind, text, fileName)
      setInferred({ schema: res.docSchema, warnings: res.warnings })
    } catch (e) {
      toast.error(e instanceof ApiError ? (e.detail ?? e.title) : String(e))
    } finally {
      setBusy(false)
    }
  }

  // A dry run of the import, for the report: what's added and what's kept.
  const outcome = useMemo(() => {
    if (!inferred) return null
    try {
      return importDesign(spec, inferred.schema, nodeId)
    } catch (e) {
      if (e instanceof OpError) return { error: e.message }
      throw e
    }
  }, [inferred, spec, nodeId])

  const submit = () => {
    if (!inferred || !outcome || 'error' in outcome) return
    apply((s) => importDesign(s, inferred.schema, nodeId).spec)
    toast.success(t('composer.import.done', { count: outcome.added }))
    onClose()
  }

  const imported = inferred?.schema.imported
  const hints = inferred?.schema.constantHints?.length ?? 0
  const bindings = spec.relations.length
  const mappings = spec.fieldMappings.filter((m) => m.state === 'active').length
  // A revision of a design already here can keep what's mapped (OC-061).
  const canUpdate = replaceAll && !!spec.docSchema.root.children?.length

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{replaceAll ? t('composer.import.titleAll') : t('composer.import.title', { name })}</DialogTitle>
          <DialogDescription>{t(replaceAll ? 'composer.import.bodyAll' : 'composer.import.body')}</DialogDescription>
        </DialogHeader>
        {inferred && reconciling ? (
          <ReimportStep incoming={inferred.schema} onBack={() => setReconciling(false)} onDone={onClose} />
        ) : inferred ? (
          <section role="status" aria-live="polite" aria-label={t('composer.import.report.title')} className="flex flex-col gap-2 text-[13px]">
            <h3 className="text-xs font-semibold uppercase tracking-wide text-muted">{t('composer.import.report.title')}</h3>
            {outcome && 'error' in outcome ? (
              <p className="text-danger">{outcome.error}</p>
            ) : (
              <ul className="flex list-disc flex-col gap-1 pl-5">
                <li>
                  {t('composer.import.report.fields', { count: outcome?.added ?? 0 })}
                  {imported ? ` · ${t(`composer.import.report.format.${imported.format}`)}` : ''}
                  {imported && imported.samples > 1 ? ` · ${t('composer.import.kind.samples', { count: imported.samples })}` : ''}
                </li>
                {imported && imported.format !== 'json-schema' && optionalCount(inferred.schema.root.children) > 0 ? (
                  <li>{t('composer.import.report.optional', { count: optionalCount(inferred.schema.root.children) })}</li>
                ) : null}
                {hints ? <li>{t('composer.import.report.constants', { count: hints })}</li> : null}
                {replaceAll && inferred.schema.reference ? <li>{t('composer.import.report.reference')}</li> : null}
                {outcome?.kept.length ? <li>{t('composer.import.report.kept', { names: outcome.kept.join(', ') })}</li> : null}
                {outcome && 'spec' in outcome && spec.typeField.enabled && !outcome.spec.typeField.enabled ? <li>{t('composer.import.report.typeFieldOff', { name: spec.typeField.name })}</li> : null}
                {replaceAll && (bindings || mappings) ? <li className="text-warning">{t('composer.import.report.replaces', { bindings, mappings })}</li> : null}
              </ul>
            )}
            {inferred.warnings.length ? (
              <div>
                <h4 className="text-xs font-medium text-muted">{t('composer.import.report.warnings')}</h4>
                <ul className="flex list-disc flex-col gap-0.5 pl-5 text-xs">
                  {inferred.warnings.map((w) => (
                    <li key={w}>{w}</li>
                  ))}
                </ul>
              </div>
            ) : null}
          </section>
        ) : (
          <div
            data-testid="import-drop"
            onDragOver={(e) => {
              if (e.dataTransfer.types.includes('Files')) {
                e.preventDefault()
                setDragging(true)
              }
            }}
            onDragLeave={() => setDragging(false)}
            onDrop={onDrop}
            className={dragging ? 'flex flex-col gap-2 rounded-md ring-2 ring-primary' : 'flex flex-col gap-2'}
          >
            <label htmlFor={textId} className="sr-only">
              {t('composer.import.label')}
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
            <div className="flex flex-wrap items-center gap-2 text-xs text-muted">
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
                  if (f) void load(f)
                  e.target.value = ''
                }}
              />
              <span>{t('composer.import.drop')}</span>
              {fileName ? (
                <span className="flex items-center gap-1 font-mono">
                  <FileJson className="size-3.5" aria-hidden />
                  {fileName}
                </span>
              ) : null}
              {sniff ? (
                <Badge className="ml-auto" tone={sniff.error ? 'danger' : 'primary'}>
                  {sniff.error ? t('composer.import.kind.invalid') : sniff.kind === 'json-schema' ? t('composer.import.kind.schema') : t('composer.import.kind.samples', { count: sampleCount(text) })}
                </Badge>
              ) : null}
            </div>
          </div>
        )}
        {inferred && reconciling ? null : (
          <DialogFooter>
            {inferred ? (
              <>
                <Button variant="secondary" onClick={() => setInferred(null)}>
                  {t('composer.import.back')}
                </Button>
                <Button variant={canUpdate ? 'secondary' : undefined} disabled={!outcome || 'error' in outcome} onClick={submit}>
                  {t(replaceAll ? 'composer.import.submitAll' : 'composer.import.submit')}
                </Button>
                {canUpdate ? <Button onClick={() => setReconciling(true)}>{t('composer.import.update')}</Button> : null}
              </>
            ) : (
              <>
                <Button variant="secondary" onClick={onClose}>
                  {t('common.cancel')}
                </Button>
                <Button disabled={!sniff?.kind || busy} onClick={() => void read()}>
                  {t('composer.import.read')}
                </Button>
              </>
            )}
          </DialogFooter>
        )}
      </DialogContent>
    </Dialog>
  )
}
