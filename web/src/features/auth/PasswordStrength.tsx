import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'

/** strength scores 0..3 from length and character variety (a lightweight heuristic). */
export function strength(pw: string): 0 | 1 | 2 | 3 {
  if (pw.length < 12) return 0
  const classes = [/[a-z]/, /[A-Z]/, /\d/, /[^A-Za-z0-9]/].filter((r) => r.test(pw)).length
  if (pw.length >= 20 || (pw.length >= 16 && classes >= 3)) return 3
  if (pw.length >= 14 || classes >= 3) return 2
  return 1
}

const levels = ['weak', 'fair', 'good', 'strong'] as const
const colors = ['bg-danger-solid', 'bg-warning-dot', 'bg-primary', 'bg-success-dot']

export function PasswordStrength({ password }: { password: string }) {
  const { t } = useTranslation()
  if (!password) return null
  const s = strength(password)
  return (
    <div className="flex items-center gap-2" aria-live="polite">
      <div className="flex flex-1 gap-1" aria-hidden>
        {[0, 1, 2, 3].map((i) => (
          <span key={i} className={cn('h-1 flex-1 rounded-full bg-surface-muted', i <= s && colors[s])} />
        ))}
      </div>
      <span className="text-xs text-muted">{t('auth.strength.label', { level: t(`auth.strength.${levels[s]}`) })}</span>
    </div>
  )
}
