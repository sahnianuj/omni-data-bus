import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/button'
import { DialogFooter } from '@/components/ui/dialog'
import { reconcileDesign, type ReconcileResult } from '@/features/mapping/api'
import { useWorkbench } from '@/features/mapping/store'
import type { DocSchema } from '@/features/mapping/types'
import { adoptDesign } from './composerOps'

/**
 * Re-importing a revised design (OC-061), a step of the import dialog: the server reconciles it
 * with the current design (fields that still exist keep their ids, so their mappings and
 * bindings stay) and proposes renames and moves, which the user can decline. Removing fields
 * that are mapped or bound needs a confirmation. Applying is one undoable edit.
 */
export function ReimportStep({ incoming, onBack, onDone }: { incoming: DocSchema; onBack: () => void; onDone: () => void }) {
  const { t } = useTranslation()
  const spec = useWorkbench((s) => s.history.present)
  const apply = useWorkbench((s) => s.apply)
  // The design this reconciles against: the one when the step opened.
  const [current] = useState(spec.docSchema)
  const [declined, setDeclined] = useState<string[]>([])
  const [res, setRes] = useState<ReconcileResult | null>(null)
  const [error, setError] = useState<unknown>(null)
  const [confirmed, setConfirmed] = useState(false)
  const seq = useRef(0)

  useEffect(() => {
    const n = ++seq.current
    reconcileDesign(current, incoming, declined)
      .then((r) => n === seq.current && (setRes(r), setError(null)))
      .catch((e: unknown) => n === seq.current && setError(e))
  }, [current, incoming, declined])

  const diff = res?.diff
  // What removing fields takes with it.
  const losing = useMemo(() => {
    if (!diff) return { mappings: 0, bindings: 0 }
    const gone = new Set(diff.removed.map((r) => r.fieldId))
    return {
      mappings: spec.fieldMappings.filter((m) => m.state !== 'rejected' && gone.has(m.targetFieldId)).length,
      bindings: spec.relations.filter((r) => r.targetFieldId && gone.has(r.targetFieldId)).length,
    }
  }, [diff, spec])
  const needsConfirm = !!diff && diff.removed.length > 0 && losing.mappings + losing.bindings > 0

  const toggle = (id: string) => setDeclined((d) => (d.includes(id) ? d.filter((x) => x !== id) : [...d, id]))
  const proposals = diff ? [...diff.renamed.map((c) => ({ ...c, kind: 'renamed' as const })), ...diff.moved.map((c) => ({ ...c, kind: 'moved' as const }))] : []
  const declinedHere = declined.filter((id) => !proposals.some((p) => p.fieldId === id))

  const submit = () => {
    if (!res) return
    apply((s) => adoptDesign(s, res.docSchema))
    toast.success(t('composer.reimport.done'))
    onDone()
  }

  return (
    <>
      <section role="status" aria-live="polite" aria-label={t('composer.reimport.title')} className="flex max-h-[55vh] flex-col gap-3 overflow-y-auto text-[13px]">
        <h3 className="text-xs font-semibold uppercase tracking-wide text-muted">{t('composer.reimport.title')}</h3>
        {error ? <ErrorState error={error} /> : null}
        {diff ? (
          <>
            {proposals.length || declinedHere.length ? (
              <fieldset className="flex flex-col gap-1">
                <legend className="text-xs font-medium text-muted">{t('composer.reimport.proposals')}</legend>
                {proposals.map((p) => (
                  <label key={p.fieldId} className="flex items-center gap-2">
                    <input type="checkbox" checked onChange={() => toggle(p.fieldId)} />
                    <span className="font-mono text-xs text-primary">{t(`composer.reimport.${p.kind}`, { from: p.from, to: p.to })}</span>
                  </label>
                ))}
                {declinedHere.map((id) => (
                  <label key={id} className="flex items-center gap-2 text-muted">
                    <input type="checkbox" checked={false} onChange={() => toggle(id)} />
                    <span className="text-xs">{t('composer.reimport.declined', { field: findPath(current, id) })}</span>
                  </label>
                ))}
              </fieldset>
            ) : null}
            <DiffList label={t('composer.reimport.added', { count: diff.added.length })} tone="text-success" items={diff.added.map((a) => `+ ${a.path} (${a.type})`)} />
            <DiffList label={t('composer.reimport.typeChanged', { count: diff.typeChanged.length })} tone="text-warning" items={diff.typeChanged.map((c) => `~ ${c.path}: ${c.from} → ${c.to}`)} />
            <DiffList label={t('composer.reimport.removed', { count: diff.removed.length })} tone="text-danger" items={diff.removed.map((r) => `− ${r.path}`)} />
            {needsConfirm ? (
              <label className="flex items-center gap-2 rounded-md border border-danger/40 p-2">
                <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} />
                <span>{t('composer.reimport.confirm', { fields: diff.removed.length, mappings: losing.mappings, bindings: losing.bindings })}</span>
              </label>
            ) : null}
            {!proposals.length && !declinedHere.length && !diff.added.length && !diff.removed.length && !diff.typeChanged.length ? <p className="text-muted">{t('composer.reimport.same')}</p> : null}
          </>
        ) : !error ? (
          <p className="text-muted">{t('composer.reimport.loading')}</p>
        ) : null}
      </section>
      <DialogFooter>
        <Button variant="secondary" onClick={onBack}>
          {t('composer.import.back')}
        </Button>
        <Button disabled={!res || (needsConfirm && !confirmed)} onClick={submit}>
          {t('composer.reimport.submit')}
        </Button>
      </DialogFooter>
    </>
  )
}

function findPath(schema: DocSchema, id: string): string {
  let out = id
  const walk = (f: DocSchema['root'], p: string) => {
    for (const c of f.children ?? []) {
      const cp = p ? `${p}.${c.name}` : c.name
      if (c.id === id) out = cp
      walk(c, cp)
      if (c.items) walk(c.items, `${cp}[]`)
    }
  }
  walk(schema.root, '')
  return out
}

function DiffList({ label, items, tone }: { label: string; items: string[]; tone: string }) {
  if (!items.length) return null
  return (
    <div>
      <h4 className="text-xs font-medium text-muted">{label}</h4>
      <ul aria-label={label} className={`flex flex-col font-mono text-xs ${tone}`}>
        {items.map((i) => (
          <li key={i}>{i}</li>
        ))}
      </ul>
    </div>
  )
}
