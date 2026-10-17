import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { HelpLink } from '@/components/HelpLink'
import { setComputed, unmap, valueSourceOf, type ValueSource } from '@/features/mapping/mappingOps'
import { useWorkbench } from '@/features/mapping/store'
import { cn } from '@/lib/utils'

/**
 * Where a target field's value comes from (OC-138): **Source** (mapped columns and their
 * transforms), **Fixed** (a constant) or **Template** (a computed field, OC-136). One line, a
 * radio group. Leaving Source drops the field's mapping after asking; undo restores it.
 */
export function ValueSourceControl({ fieldId, onFixed }: { fieldId: string; onFixed?: () => void }) {
  const { t } = useTranslation()
  const spec = useWorkbench((s) => s.history.present)
  const apply = useWorkbench((s) => s.apply)
  const current = valueSourceOf(spec, fieldId)
  const [pending, setPending] = useState<ValueSource | null>(null)
  const [fixedOpen, setFixedOpen] = useState(false)
  const shown = fixedOpen && current === 'source' ? 'fixed' : current
  const mapped = spec.fieldMappings.find((m) => m.targetFieldId === fieldId && m.state === 'active' && m.sources.length > 0)

  const change = (to: ValueSource) => {
    setFixedOpen(false)
    if (to === current) return
    if (to === 'template') apply((s) => setComputed(s, fieldId, { lang: 'template', expr: '' }))
    else if (to === 'fixed') {
      if (current === 'template') apply((s) => setComputed(s, fieldId, null))
      setFixedOpen(true)
      onFixed?.()
    } else apply((s) => unmap(s, fieldId))
  }
  const pick = (to: ValueSource) => (current === 'source' && mapped && to !== 'source' ? setPending(to) : change(to))

  return (
    <div className="flex items-center gap-1.5">
    <div role="radiogroup" aria-label={t('computed.valueSource')} className="inline-flex w-fit rounded-[var(--radius-control)] border border-border bg-surface-muted p-0.5 text-xs">
      {(['source', 'fixed', 'template'] as const).map((k) => (
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
