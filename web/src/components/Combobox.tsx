import { Command } from 'cmdk'
import { Check, ChevronsUpDown } from 'lucide-react'
import { useId, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { cn } from '@/lib/utils'

export type ComboOption = { value: string; label?: string; hint?: ReactNode }

type Props = {
  label: string
  value: string
  options: ComboOption[]
  onChange: (value: string) => void
  placeholder: string
  disabled?: boolean
  loading?: boolean
  /** Shown on the trigger after the value, e.g. a "will be created" badge. */
  adornment?: ReactNode
}

/** A labelled, searchable single-select (Popover + cmdk). */
export function Combobox({ label, value, options, onChange, placeholder, disabled, loading, adornment }: Props) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const id = useId()
  const current = options.find((o) => o.value === value)
  return (
    <div className="flex min-w-0 flex-1 flex-col gap-1.5">
      <label htmlFor={id} className="text-xs font-medium text-muted">
        {label}
      </label>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <button
            id={id}
            type="button"
            role="combobox"
            aria-expanded={open}
            disabled={disabled}
            className="flex h-9 w-full items-center gap-2 rounded-[var(--radius-control)] border border-border-strong bg-surface px-3 text-left text-sm disabled:cursor-not-allowed disabled:opacity-50"
          >
            <span className={cn('min-w-0 flex-1 truncate', !value && 'text-subtle')}>{value ? (current?.label ?? value) : loading ? t('common.loading') : placeholder}</span>
            {adornment}
            <ChevronsUpDown className="size-3.5 shrink-0 text-subtle" aria-hidden />
          </button>
        </PopoverTrigger>
        <PopoverContent align="start" className="w-[var(--radix-popover-trigger-width)] min-w-[240px] p-0">
          <Command label={label}>
            <Command.Input placeholder={t('targets.filter')} className="h-9 w-full border-b border-border bg-transparent px-3 text-sm outline-none" />
            <Command.List className="max-h-64 overflow-y-auto p-1">
              <Command.Empty className="px-3 py-4 text-center text-xs text-muted">{t('targets.noOptions')}</Command.Empty>
              {options.map((o) => (
                <Command.Item
                  key={o.value}
                  value={o.value}
                  onSelect={() => {
                    onChange(o.value)
                    setOpen(false)
                  }}
                  className="flex cursor-default items-center gap-2 rounded-md px-2 py-1.5 text-sm data-[selected=true]:bg-surface-muted"
                >
                  <Check className={cn('size-3.5', o.value === value ? 'opacity-100' : 'opacity-0')} aria-hidden />
                  <span className="truncate">{o.label ?? o.value}</span>
                  {o.hint ? <span className="ml-auto text-xs text-subtle">{o.hint}</span> : null}
                </Command.Item>
              ))}
            </Command.List>
          </Command>
        </PopoverContent>
      </Popover>
    </div>
  )
}
