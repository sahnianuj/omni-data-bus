import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { HelpLink } from '@/components/HelpLink'
import { fieldsById } from '@/features/mapping/embeds'
import { setComputed, unmap, valueSourceOf, type ValueSource } from '@/features/mapping/mappingOps'
import { canHoldNow, defaultNow, setNow } from '@/features/mapping/nowSource'
import { useWorkbench } from '@/features/mapping/store'
import { cn } from '@/lib/utils'

/**
 * Where a target field's value comes from (OC-138): **Source** (mapped columns and their
 * transforms), **Fixed** (a constant) or **Template** (a computed field, OC-136). One line, a
 * radio group. Leaving Source drops the field's mapping after asking; undo restores it. A field
 * that can hold a date also offers **Now**, the current date and time (OC-179).
 */
export function ValueSourceControl({ fieldId, onFixed, fixed, onFixedChange }: { fieldId: string; onFixed?: () => void; fixed?: boolean; onFixedChange?: (open: boolean) => void }) {
  const { t } = useTranslation()
  const spec = useWorkbench((s) => s.history.present)
  const apply = useWorkbench((s) => s.apply)
  const current = valueSourceOf(spec, fieldId)
  const [pending, setPending] = useState<ValueSource | null>(null)
  // Fixed before a value is set: the inspector shows its value box (controlled when it passes fixed).
  const [localFixed, setLocalFixed] = useState(false)
  const fixedOpen = fixed ?? localFixed
  const setFixedOpen = (open: boolean) => {
    setLocalFixed(open)
    onFixedChange?.(open)
  }
  const shown = fixedOpen && current === 'source' ? 'fixed' : current
  const mapped = spec.fieldMappings.find((m) => m.targetFieldId === fieldId && m.state === 'active' && m.sources.length > 0)

  const change = (to: ValueSource) => {
    setFixedOpen(false)
    if (to === current) return
    if (to === 'template') apply((s) => setComputed(s, fieldId, { lang: 'template', expr: '' }))
    else if (to === 'now') apply((s) => setNow(s, fieldId, defaultNow(s, fieldId)))
    else if (to === 'fixed') {
      if (current === 'template') apply((s) => setComputed(s, fieldId, null))
      else if (current === 'now') apply((s) => unmap(s, fieldId))
      setFixedOpen(true)
      onFixed?.()
    } else apply((s) => unmap(s, fieldId))
  }
  const pick = (to: ValueSource) => (current === 'source' && mapped && to !== 'source' ? setPending(to) : change(to))

  return (
    <div className="flex items-center gap-1.5">
    <div role="radiogroup" aria-label={t('computed.valueSource')} className="inline-flex w-fit rounded-[var(--radius-control)] border border-border bg-surface-muted p-0.5 text-xs">
      {(canHoldNow(fieldsById(spec).get(fieldId)?.type) ? (['source', 'fixed', 'template', 'now'] as const) : (['source', 'fixed', 'template'] as const)).map((k) => (
        <button
          key={k}
          type="button"
          role="radio"
          aria-checked={shown === k}
          title={t(`computed.sourceHint.${k}`)}
          onClick={() => pick(k)}
          className={cn('rounded px-2.5 py-1', shown === k ? 'bg-surface font-medium shadow-sm' : 'text-muted hover:text-foreground')}
        >
          {t(`computed.source.${k}`)}
        </button>
      ))}
      <ConfirmDialog
        open={pending !== null}
        onOpenChange={(o) => !o && setPending(null)}
        title={t('computed.leaveSourceTitle')}
        description={t('computed.leaveSource', { columns: mapped?.sources.map((s) => s.column).join(', ') ?? '' })}
        confirmLabel={t('computed.leaveSourceConfirm')}
        onConfirm={() => {
          if (pending) change(pending)
          setPending(null)
        }}
      />
    </div>
    <HelpLink topic="computed" />
    </div>
  )
}

/**
 * The value source an inspector shows for a field: what fills it, or Fixed while the user is
 * about to set a constant (pass fixed and onFixedChange to ValueSourceControl). Per field.
 */
export function useValueMode(fieldId: string | undefined) {
  const spec = useWorkbench((s) => s.history.present)
  const [fixedFor, setFixedFor] = useState<string | null>(null)
  const current = fieldId ? valueSourceOf(spec, fieldId) : 'source'
  const fixedOpen = !!fieldId && fixedFor === fieldId
  return {
    mode: (current === 'source' && fixedOpen ? 'fixed' : current) as ValueSource,
    fixed: fixedOpen,
    onFixedChange: (open: boolean) => setFixedFor(open && fieldId ? fieldId : null),
  }
}
