import { Monitor, Moon, Sun } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useTheme, type ThemePreference } from '@/app/ThemeProvider'
import { cn } from '@/lib/utils'

const options: { value: ThemePreference; icon: typeof Sun; key: string }[] = [
  { value: 'light', icon: Sun, key: 'theme.light' },
  { value: 'dark', icon: Moon, key: 'theme.dark' },
  { value: 'system', icon: Monitor, key: 'theme.system' },
]

export function ThemeMenu() {
  const { t } = useTranslation()
  const { preference, setPreference } = useTheme()
  return (
    <div role="radiogroup" aria-label={t('theme.toggle')} className="flex rounded-md bg-surface-muted p-0.5">
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={preference === o.value}
          aria-label={t(o.key)}
          onClick={() => setPreference(o.value)}
          className={cn(
            'flex size-7 items-center justify-center rounded text-subtle hover:text-foreground',
            preference === o.value && 'bg-surface text-foreground shadow-sm',
          )}
        >
          <o.icon className="size-3.5" aria-hidden />
        </button>
      ))}
    </div>
  )
}
