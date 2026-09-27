import { LogOut } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router'
import { useSession, useSignOut } from './api'

function initials(name: string) {
  return name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((p) => p[0]?.toUpperCase())
    .join('')
}

export function UserMenu() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const session = useSession()
  const signOut = useSignOut()
  const s = session.data
  if (!s) return null
  return (
    <div className="flex items-center gap-2.5">
      <span aria-hidden className="flex size-[30px] shrink-0 items-center justify-center rounded-full bg-primary-soft text-xs font-semibold text-primary-soft-foreground">
        {initials(s.user.displayName || s.user.email)}
      </span>
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="truncate text-[13px] font-medium">{s.user.displayName}</span>
        <span className="truncate text-xs text-subtle">{t(`auth.roles.${s.role}`)}</span>
      </span>
      <button
        type="button"
        aria-label={t('auth.signOut')}
        onClick={() => signOut.mutate(undefined, { onSettled: () => navigate('/signin', { replace: true }) })}
        className="flex size-8 items-center justify-center rounded-md text-subtle hover:bg-surface-muted hover:text-foreground"
      >
        <LogOut className="size-4" aria-hidden />
      </button>
    </div>
  )
}
