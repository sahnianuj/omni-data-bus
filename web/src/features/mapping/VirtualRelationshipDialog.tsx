import { Plus, Trash2, X } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ErrorState } from '@/components/ErrorState'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useSchema } from '@/features/discovery/api'
import { detectCardinality, useCreateRelationship, useDeleteRelationship, useRelationships } from './relationsApi'
import { useWorkbench } from './store'
import type { RelationshipDetection, RelationshipInput } from './types'

const selectCls = 'h-8 w-full rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 text-[13px]'
type Cardinality = 'many-to-one' | 'one-to-one' | 'one-to-many'

/**
 * Virtual relationships (OC-042): joins the source has no foreign key for, stored with the
 * project. Pick both tables and column pairs, detect the cardinality by sampling the data (the
 * user may override it; a contradiction is warned about), then save: the relationship shows in
 * the Related tables with a "virtual" badge and embeds like a foreign key.
 */
export function VirtualRelationshipDialog({ onClose, initialFrom, initialTo }: { onClose: () => void; initialFrom?: string; initialTo?: string }) {
  const { t } = useTranslation()
  const view = useWorkbench((s) => s.view)
  const list = useRelationships(view.projectId)
  const create = useCreateRelationship(view.projectId)
  const remove = useDeleteRelationship(view.projectId)
  const snap = useSchema(`${view.projectId}::source`)
  const tables = useMemo(
    () => (snap.data?.schemas ?? []).flatMap((s) => s.tables.map((tb) => ({ key: `${s.name}.${tb.name}`, schema: s.name, table: tb.name, columns: (tb.columns ?? []).map((c) => c.name) }))),
    [snap.data],
  )
  const [from, setFrom] = useState(initialFrom ?? `${view.sourceSchema}.${view.sourceTable}`)
  const [to, setTo] = useState(initialTo ?? '')
  const [pairs, setPairs] = useState<{ from: string; to: string }[]>([{ from: '', to: '' }])
  const [name, setName] = useState('')
  const [cardinality, setCardinality] = useState<Cardinality>('many-to-one')
  const [detected, setDetected] = useState<RelationshipDetection | null>(null)
  const [detecting, setDetecting] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const fromT = tables.find((x) => x.key === from)
  const toT = tables.find((x) => x.key === to)

  const input = (): RelationshipInput => ({
    name: name.trim() || undefined,
    from: { schema: fromT?.schema ?? '', table: fromT?.table ?? '', columns: pairs.map((p) => p.from) },
    to: { schema: toT?.schema ?? '', table: toT?.table ?? '', columns: pairs.map((p) => p.to) },
    cardinality,
  })
  const complete = !!fromT && !!toT && pairs.every((p) => p.from && p.to)

  const detect = async () => {
    setDetecting(true)
    setError(null)
    try {
      const d = await detectCardinality(view.projectId, input())
      setDetected(d)
      if (d.suggested !== 'unknown') setCardinality(d.suggested)
    } catch (e) {
      setError(e)
    } finally {
      setDetecting(false)
    }
  }

  const save = async () => {
    setError(null)
    try {
      await create.mutateAsync({ ...input(), ...(detected ? { detected } : {}) })
      setTo('')
      setPairs([{ from: '', to: '' }])
      setName('')
      setDetected(null)
    } catch (e) {
      setError(e)
    }
  }

  const contradicts = detected && detected.suggested !== 'unknown' && cardinality !== 'one-to-many' && detected.maxPerKey > 1

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t('virtual.title')}</DialogTitle>
          <DialogDescription>{t('virtual.description')}</DialogDescription>
        </DialogHeader>
        <ul className="grid gap-1 text-[13px]" aria-label={t('virtual.list')}>
          {(list.data ?? []).length === 0 ? <li className="text-xs text-subtle">{t('virtual.none')}</li> : null}
          {(list.data ?? []).map((r) => (
            <li key={r.id} className="flex items-center gap-2 rounded border border-border px-2 py-1">
              <span className="min-w-0 flex-1 truncate">{r.name}</span>
              <Badge>{r.cardinality}</Badge>
              <button type="button" className="rounded p-0.5 text-subtle hover:text-danger" aria-label={t('virtual.delete', { name: r.name })} onClick={() => remove.mutate(r.id)}>
                <Trash2 className="size-3.5" />
              </button>
            </li>
          ))}
        </ul>
        <div className="grid gap-3 border-t border-border pt-3 text-[13px]">
          <div className="grid grid-cols-2 gap-2">
            <label className="grid gap-1">
              <span className="text-muted">{t('virtual.from')}</span>
              <select className={selectCls} value={from} onChange={(e) => { setFrom(e.target.value); setDetected(null) }}>
                {tables.map((x) => <option key={x.key} value={x.key}>{x.key}</option>)}
              </select>
            </label>
            <label className="grid gap-1">
              <span className="text-muted">{t('virtual.to')}</span>
              <select className={selectCls} value={to} onChange={(e) => { setTo(e.target.value); setDetected(null) }}>
                <option value="">{t('virtual.pickTable')}</option>
                {tables.map((x) => <option key={x.key} value={x.key}>{x.key}</option>)}
              </select>
            </label>
          </div>
          {pairs.map((p, i) => (
            <div key={i} className="grid grid-cols-[1fr_1fr_auto] items-end gap-2">
              <select aria-label={t('virtual.fromColumn', { n: i + 1 })} className={selectCls} value={p.from} onChange={(e) => setPairs(pairs.map((x, j) => (j === i ? { ...x, from: e.target.value } : x)))}>
                <option value="">{t('virtual.pickColumn')}</option>
                {(fromT?.columns ?? []).map((c) => <option key={c} value={c}>{c}</option>)}
              </select>
              <select aria-label={t('virtual.toColumn', { n: i + 1 })} className={selectCls} value={p.to} onChange={(e) => setPairs(pairs.map((x, j) => (j === i ? { ...x, to: e.target.value } : x)))}>
                <option value="">{t('virtual.pickColumn')}</option>
                {(toT?.columns ?? []).map((c) => <option key={c} value={c}>{c}</option>)}
              </select>
              <button type="button" disabled={pairs.length === 1} className="h-8 rounded p-1 text-subtle hover:text-foreground disabled:opacity-40" aria-label={t('virtual.removePair')} onClick={() => setPairs(pairs.filter((_, j) => j !== i))}>
                <X className="size-3.5" />
              </button>
            </div>
          ))}
          <Button size="sm" variant="ghost" className="justify-self-start" onClick={() => setPairs([...pairs, { from: '', to: '' }])}>
            <Plus className="size-3.5" aria-hidden />
            {t('virtual.addPair')}
          </Button>
          <div className="grid grid-cols-[1fr_auto] items-end gap-2">
            <label className="grid gap-1">
              <span className="text-muted">{t('virtual.cardinality')}</span>
              <select className={selectCls} value={cardinality} onChange={(e) => setCardinality(e.target.value as Cardinality)}>
                <option value="many-to-one">{t('virtual.m2o')}</option>
                <option value="one-to-one">{t('virtual.o2o')}</option>
                <option value="one-to-many">{t('virtual.o2m')}</option>
              </select>
            </label>
            <Button variant="secondary" loading={detecting} disabled={!complete} onClick={() => void detect()}>
              {t('virtual.detect')}
            </Button>
          </div>
          {detected ? (
            <p className="text-xs text-muted" data-testid="virtual-detected">
              {detected.suggested === 'unknown'
                ? detected.warning
                : t('virtual.detected', { suggested: detected.suggested, max: detected.maxPerKey, sampled: detected.sampledRows })}
            </p>
          ) : null}
          {contradicts ? <p className="text-xs text-warning">{t('virtual.contradicts', { max: detected.maxPerKey, cardinality })}</p> : null}
          <label className="grid gap-1">
            <span className="text-muted">{t('virtual.name')}</span>
            <Input value={name} placeholder={t('virtual.namePlaceholder')} onChange={(e) => setName(e.target.value)} />
          </label>
        </div>
        {error ? <ErrorState error={error} /> : null}
        <DialogFooter>
          <Button variant="secondary" onClick={onClose}>
            {t('common.close')}
          </Button>
          <Button loading={create.isPending} disabled={!complete} onClick={() => void save()}>
            {t('virtual.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
