import { useTranslation } from 'react-i18next'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'
import { useConnectors } from './api'

/** Database types planned after 1.0, shown disabled so users see what's coming. */
export const comingSoon: Record<'source' | 'target', string[]> = {
  source: ['MySQL', 'SQL Server', 'Oracle'],
  target: [],
}

type Props = { role: 'source' | 'target'; value: string | null; onChange: (type: string) => void }

/** Cards for choosing a database type (OC-008 AC2): available connectors from the API plus coming-soon ones. */
export function ConnectorPicker({ role, value, onChange }: Props) {
  const { t } = useTranslation()
  const catalog = useConnectors()
  if (catalog.status === 'pending') return <Skeleton className="h-14 w-full" />
  const available = (role === 'source' ? catalog.data?.sources : catalog.data?.targets) ?? []
  const upcoming = comingSoon[role].filter((name) => !available.some((a) => a.displayName === name))
  return (
    <div role="radiogroup" aria-label={t('connectors.label')} className="grid grid-cols-2 gap-2 sm:grid-cols-4">
      {available.map((c) => {
        const selected = value === c.type
        return (
          <button
            key={c.type}
            type="button"
            role="radio"
            aria-checked={selected}
            onClick={() => onChange(c.type)}
            className={cn(
              'flex h-14 flex-col items-center justify-center gap-0.5 rounded-lg border bg-surface',
              selected ? 'border-2 border-primary' : 'border-border hover:border-border-strong',
            )}
          >
            <span className="text-[13px] font-semibold">{c.displayName}</span>
            <span className={cn('text-[11px]', selected ? 'text-primary-soft-foreground' : 'text-subtle')}>
              {selected ? t('connectors.selected') : t('connectors.available')}
            </span>
          </button>
        )
      })}
      {upcoming.map((name) => (
        <button key={name} type="button" role="radio" aria-checked={false} disabled className="flex h-14 flex-col items-center justify-center gap-0.5 rounded-lg border border-border bg-background">
          <span className="text-[13px] font-medium text-subtle">{name}</span>
          <span className="text-[11px] text-subtle">{t('connectors.comingSoon')}</span>
        </button>
      ))}
    </div>
  )
}
