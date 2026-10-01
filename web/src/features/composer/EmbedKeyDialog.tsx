import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { pathsById } from '@/features/docschema/docschemaOps'
import { useSourceTable } from '@/features/discovery/api'
import { useWorkbench } from '@/features/mapping/store'
import { expandForeignKey, nodeOf, OpError, singularName, type ForeignKey } from './composerOps'

/**
 * "Embed products here…" (OC-064): replaces a foreign-key field (productId) with the object it
 * refers to, or adds the object alongside it, bound many-to-one through the key, with fields
 * from the chosen columns; the caller auto-maps the new object's scope.
 */
export function EmbedKeyDialog({ fieldId, fk, onClose, onDone }: { fieldId: string; fk: ForeignKey; onClose: () => void; onDone: (objectId: string) => void }) {
  const { t } = useTranslation()
  const view = useWorkbench((s) => s.view)
  const spec = useWorkbench((s) => s.history.present)
  const apply = useWorkbench((s) => s.apply)
  const table = useSourceTable(`${view.projectId}::source`, fk.table.schema, fk.table.name)
  const field = nodeOf(spec, fieldId)
  // The customer's design has the key field: keep it, or the documents lose it (OC-071).
  const inDesign = (() => {
    const path = pathsById(spec.docSchema).get(fieldId)
    return !!path && !!spec.docSchema.imported?.fields?.some((f) => f.path === path)
  })()
  const [mode, setMode] = useState<'replace' | 'alongside'>(inDesign ? 'alongside' : 'replace')
  const [name, setName] = useState(singularName(fk.table.name))
  const [picked, setPicked] = useState<string[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const nameId = useId()
  const columns = table.data?.columns ?? []
  // By default every column but the key itself (the id is the field being expanded).
  const chosen = picked ?? columns.map((c) => c.name).filter((c) => !fk.join.some((j) => j.child === c))

  const submit = () => {
    try {
      let id = ''
      apply((s) => {
        const res = expandForeignKey(s, fieldId, fk, { mode, name: name.trim(), columns: columns.filter((c) => chosen.includes(c.name)) })
        id = res.id
        return res.spec
      })
      onDone(id)
    } catch (e) {
      if (e instanceof OpError) setError(e.message)
      else throw e
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('composer.embedKey.title', { table: fk.table.name })}</DialogTitle>
          <DialogDescription>{t('composer.embedKey.body', { field: field?.name ?? '', join: fk.join.map((j) => `${j.parent} = ${j.child}`).join(', ') })}</DialogDescription>
        </DialogHeader>
        <fieldset className="flex flex-col gap-1.5 text-[13px]">
          <legend className="mb-1 text-xs font-medium text-muted">{t('composer.embedKey.mode')}</legend>
          <label className="flex items-center gap-2">
            <input type="radio" name="embed-mode" checked={mode === 'replace'} onChange={() => setMode('replace')} />
            {t('composer.embedKey.replace', { field: field?.name ?? '' })}
          </label>
          <label className="flex items-center gap-2">
            <input type="radio" name="embed-mode" checked={mode === 'alongside'} onChange={() => setMode('alongside')} />
            {t('composer.embedKey.alongside', { field: field?.name ?? '' })}
          </label>
          {inDesign ? <p className={mode === 'replace' ? 'text-xs text-warning' : 'text-xs text-muted'}>{t('composer.embedKey.inDesign', { field: field?.name ?? '' })}</p> : null}
        </fieldset>
        <label htmlFor={nameId} className="flex flex-col gap-1 text-xs font-medium text-muted">
          {t('composer.embedKey.name')}
          <Input id={nameId} value={name} onChange={(e) => setName(e.target.value)} className="h-8" />
        </label>
        <fieldset className="flex flex-col gap-1 text-[13px]">
          <legend className="mb-1 text-xs font-medium text-muted">{t('composer.embedKey.columns', { table: fk.table.name })}</legend>
          <ul className="flex max-h-48 flex-col gap-1 overflow-y-auto">
            {columns.map((c) => (
              <li key={c.name}>
                <label className="flex items-center gap-2 font-mono text-xs">
                  <input type="checkbox" checked={chosen.includes(c.name)} onChange={(e) => setPicked(e.target.checked ? [...chosen, c.name] : chosen.filter((x) => x !== c.name))} />
                  {c.name}
                </label>
              </li>
            ))}
          </ul>
        </fieldset>
        {error ? (
          <p role="alert" className="text-xs text-danger">
            {error}
          </p>
        ) : null}
        <DialogFooter>
          <Button variant="secondary" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button disabled={!name.trim() || !chosen.length} onClick={submit}>
            {t('composer.embedKey.submit')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
