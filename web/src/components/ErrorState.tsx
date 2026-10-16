import { AlertTriangle, Copy, Home } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Link, useInRouterContext, useLocation } from 'react-router'
import { ApiError } from '@/lib/api/client'
import { Button } from './ui/button'

/**
 * The server or the gateway in front of it failed (e.g. 502 Bad Gateway), or the request never
 * reached it: nothing on this screen can fix that, so the user is offered the way home.
 */
export const isServerFailure = (error: unknown) =>
  error instanceof ApiError ? error.status >= 500 : error instanceof TypeError

function HomeButton() {
  const { t } = useTranslation()
  const { pathname } = useLocation()
  if (pathname === '/') return null
  return (
    <Button asChild variant="secondary" size="sm">
      <Link to="/">
        <Home aria-hidden />
        {t('common.backHome')}
      </Link>
    </Button>
  )
}

export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const { t } = useTranslation()
  const inRouter = useInRouterContext()
  const apiError = error instanceof ApiError ? error : null
  const title = apiError?.title ?? t('common.somethingWrong')
  const detail = apiError?.detail ?? (error instanceof Error ? error.message : undefined)
  return (
    <div role="alert" className="flex flex-col items-center gap-3 rounded-[var(--radius-card)] border border-border bg-surface px-6 py-12 text-center">
      <AlertTriangle className="size-7 text-danger" aria-hidden />
      <h2 className="text-base font-semibold">{title}</h2>
      {detail ? <p className="max-w-md text-sm text-muted">{detail}</p> : null}
      {apiError?.requestId ? (
        <button
          type="button"
          className="flex items-center gap-1.5 font-mono text-xs text-subtle hover:text-foreground"
          onClick={() => void navigator.clipboard?.writeText(apiError.requestId ?? '')}
        >
          {t('common.requestId')}: {apiError.requestId}
          <Copy className="size-3" aria-label={t('common.copy')} />
        </button>
      ) : null}
      {onRetry || (inRouter && isServerFailure(error)) ? (
        <div className="flex flex-wrap justify-center gap-2">
          {onRetry ? (
            <Button variant="secondary" size="sm" onClick={onRetry}>
              {t('common.retry')}
            </Button>
          ) : null}
          {inRouter && isServerFailure(error) ? <HomeButton /> : null}
        </div>
      ) : null}
    </div>
  )
}
