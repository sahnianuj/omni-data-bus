import { ChevronRight, ScrollText } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { PageHeader } from '@/components/PageHeader'
import { hasRole } from '@/features/auth/Can'
import { useSession } from '@/features/auth/api'

export function SettingsPage() {
  const { t } = useTranslation()
  const session = useSession()
  return (
    <div className="flex flex-col gap-6 px-10 py-8">
      <PageHeader title={t('settings.title')} description={t('settings.description')} />
      {hasRole(session.data?.role, 'owner') ? (
        <Link
          to="/settings/audit"
          className="flex max-w-md items-center gap-3 rounded-[var(--radius-card)] border border-border bg-surface p-4 hover:bg-surface-muted"
        >
          <ScrollText className="size-5 text-muted" aria-hidden />
          <span className="flex flex-1 flex-col">
            <span className="text-sm font-medium">{t('audit.title')}</span>
            <span className="text-[13px] text-muted">{t('audit.description')}</span>
          </span>
          <ChevronRight className="size-4 text-muted" aria-hidden />
        </Link>
      ) : null}
    </div>
  )
}
