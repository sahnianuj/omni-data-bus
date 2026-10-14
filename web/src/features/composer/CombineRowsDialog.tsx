import { useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useSourceTable } from '@/features/discovery/api'
import { useWorkbench } from '@/features/mapping/store'
import { combinable, combineRows, pluralName, sharedNames } from './composerOps'
import { toComposed } from './modeSwitch'
import { boundRelations } from './scope'

/**
 * "Combine rows with the same key" (OC-066), from KEY_MAY_COLLIDE: rows that would collide on
 * the key become one document, grouped by the key's columns, each row an entry of an array.
 * Fields mapped from other columns move into the array's item. From ARRAY_REPEATS_ROOT, relationId
 * is the array bound back to the driving table: it's offered (and chosen) to take the rows.
 */
export function CombineRowsDialog({ columns, relationId, onClose, onDone }: { columns: string[]; relationId?: string; onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation()
  const view = useWorkbench((s) => s.view)
  const spec = useWorkbench((s) => s.history.present)
  const apply = useWorkbench((s) => s.apply)
  const own = { schema: view.sourceSchema, name: view.sourceTable }
  const driving = spec.root?.table ?? own
  const table = useSourceTable(`${view.projectId}::source`, driving.schema, driving.name)
  const nameId = useId()
  // Unbound arrays at the root (an imported design's products[]) can take the rows, and so can
  // the array bound to the driving table that the fix is about (its binding becomes the group's).
  const repeating = spec.relations.find((r) => r.id === relationId)?.targetFieldId
  const arrays = useMemo(() => {
    const bound = boundRelations(spec)
    return (spec.docSchema.root.children ?? []).filter((f) => f.type === 'array' && (!bound.has(f.id) || f.id === repeating))
  }, [spec, repeating])
  const [target, setTarget] = useState<string>(repeating ?? arrays[0]?.id ?? '')
  const [name, setName] = useState(pluralName(driving.name))
  const grouped = new Set(columns)
  const candidates = combinable(spec, columns)
  const [move, setMove] = useState<string[]>(() => candidates.filter((f) => !sharedNames.test(f.name)).map((f) => f.id))
  const orderBy = (table.data?.primaryKey ?? []).filter((k) => !grouped.has(k)).map((column) => ({ column }))

  const submit = () => {
    apply((s) => combineRows(s.mode === 'composed' ? s : toComposed(s, own), { groupBy: columns, driving, arrayName: name.trim(), ...(target ? { arrayId: target } : {}), orderBy, move }).spec)
    toast.success(t('composer.combine.done', { columns: columns.join(', ') }))
    onDone()
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('composer.combine.title')}</DialogTitle>
          <DialogDescription>{t('composer.combine.body', { columns: columns.join(', '), table: driving.name })}</DialogDescription>
        </DialogHeader>
        <fieldset className="flex flex-col gap-1.5 text-[13px]">
          <legend className="mb-1 text-xs font-medium text-muted">{t('composer.combine.array')}</legend>
          {arrays.map((a) => (
            <label key={a.id} className="flex items-center gap-2">
              <input type="radio" name="combine-array" checked={target === a.id} onChange={() => setTarget(a.id)} />
              {t('composer.combine.existing', { name: a.name })}
            </label>
          ))}
          <label className="flex items-center gap-2">
            <input type="radio" name="combine-array" checked={target === ''} onChange={() => setTarget('')} aria-label={t('composer.combine.new')} />
            {t('composer.combine.new')}
          </label>
          <label htmlFor={nameId} className="sr-only">
            {t('composer.combine.name')}
          </label>
          <Input id={nameId} disabled={target !== ''} value={name} onChange={(e) => setName(e.target.value)} className="ml-6 h-8 w-56" />
        </fieldset>
        {candidates.length ? (
          <fieldset className="flex flex-col gap-1 text-[13px]">
            <legend className="mb-1 text-xs font-medium text-muted">{t('composer.combine.moving')}</legend>
            {candidates.map((f) => (
              <label key={f.id} className="flex items-center gap-2">
                <input type="checkbox" checked={move.includes(f.id)} onChange={(e) => setMove((m) => (e.target.checked ? [...m, f.id] : m.filter((x) => x !== f.id)))} />
                <span className="font-mono text-xs">{f.name}</span>
              </label>
            ))}
            <p className="text-xs text-muted">{t('composer.combine.stays')}</p>
          </fieldset>
        ) : null}
        <DialogFooter>
          <Button variant="secondary" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button disabled={target === '' && !name.trim()} onClick={submit}>
            {t('composer.combine.submit')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
