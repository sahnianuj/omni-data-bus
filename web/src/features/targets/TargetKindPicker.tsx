import { Database, FolderOpen } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'
import { useTargetKinds } from './kinds'

type Props = { value: string | null; onChange: (kind: string) => void }

const icons: Record<string, typeof Database> = { couchbase: Database, directory: FolderOpen }

/**
 * The Target step's kind picker (OC-036): a Couchbase cluster or a directory of JSON files. A
 * kind the server has switched off stays visible, disabled, with the administrator's fix.
 */
export function TargetKindPicker({ value, onChange }: Props) {
  const { t } = useTranslation()
  const kinds = useTargetKinds()
  if (kinds.status === 'pending') return <Skeleton className="h-20 w-full" />
  return (
    <div
      role="radiogroup"
      aria-label={t('targetKinds.label')}
      className="grid grid-cols-1 gap-2 sm:grid-cols-2"
    >
      {(kinds.data ?? []).map((k) => {
        const selected = value === k.type
        const Icon = icons[k.type] ?? Database
        const title = t(`targetKinds.${k.type}.title`, { defaultValue: k.displayName })
        return (
          <button
            key={k.type}
            type="button"
            role="radio"
            aria-checked={selected}
            aria-describedby={`kind-${k.type}-body`}
            disabled={!k.enabled}
            onClick={() => onChange(k.type)}
            className={cn(
              'flex items-start gap-3 rounded-lg border bg-surface p-3 text-left disabled:cursor-not-allowed disabled:bg-background',
              selected ? 'border-2 border-primary' : 'border-border hover:border-border-strong',
            )}
          >
            <Icon
              className={cn('mt-0.5 size-5 shrink-0', selected ? 'text-primary' : 'text-muted')}
              aria-hidden
            />
            <span className="flex flex-col gap-0.5">
              <span className="text-[13px] font-semibold">{title}</span>
              <span id={`kind-${k.type}-body`} className="text-xs text-muted">
                {k.enabled ? t(`targetKinds.${k.type}.body`, { defaultValue: '' }) : k.disabledReason}
              </span>
            </span>
          </button>
        )
      })}
    </div>
  )
}
