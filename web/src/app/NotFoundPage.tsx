import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { EmptyState } from '@/components/EmptyState'
import { Button } from '@/components/ui/button'

export function NotFoundPage() {
  const { t } = useTranslation()
  return (
    <div className="px-10 py-8">
      <EmptyState
        title={t('common.notFoundTitle')}
        body={t('common.notFoundBody')}
        action={
          <Button asChild variant="secondary">
            <Link to="/">{t('common.backHome')}</Link>
          </Button>
        }
      />
    </div>
  )
}
