import { AlertTriangle, Check, ChevronRight, Copy, RotateCcw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { isRouteErrorResponse, Link, useLocation, useRouteError } from 'react-router'
import { Button } from '@/components/ui/button'
import { NotFoundPage } from './NotFoundPage'

function describe(error: unknown): { message: string; stack?: string } {
  if (isRouteErrorResponse(error)) return { message: `${error.status} ${error.statusText}`.trim() }
  if (error instanceof Error) return { message: error.message, stack: error.stack }
  return { message: String(error) }
}

/**
 * Router `errorElement`: replaces React Router's default "Unexpected Application Error!" screen.
 * Users see a calm recovery page; the technical detail stays tucked away (open by default in dev)
 * and can be copied for a bug report.
 */
export function RouteErrorPage({ fullPage = false }: { fullPage?: boolean }) {
  const { t } = useTranslation()
  const error = useRouteError()
  const location = useLocation()
  const [copied, setCopied] = useState(false)

  if (isRouteErrorResponse(error) && error.status === 404) return <NotFoundPage />

  const { message, stack } = describe(error)
  const report = [
    `${t('errorPage.path')}: ${location.pathname}${location.search}`,
    `${t('errorPage.time')}: ${new Date().toISOString()}`,
    '',
    stack ?? message,
  ].join('\n')

  const copy = () => {
    void navigator.clipboard?.writeText(report).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    })
  }

  return (
    <div
      className={
        fullPage ? 'flex min-h-screen items-center justify-center bg-background px-6 py-12' : 'px-10 py-8'
      }
    >
      <div
        role="alert"
        className="mx-auto flex max-w-xl flex-col items-center gap-4 rounded-[var(--radius-card)] border border-border bg-surface px-8 py-12 text-center"
      >
        <div className="flex size-12 items-center justify-center rounded-full bg-danger-soft">
          <AlertTriangle className="size-6 text-danger" aria-hidden />
        </div>
        <div className="flex flex-col gap-1.5">
          <h1 className="text-lg font-semibold">{t('errorPage.title')}</h1>
          <p className="text-sm text-muted">{t('errorPage.body')}</p>
        </div>
        <div className="flex flex-wrap justify-center gap-2">
          <Button onClick={() => window.location.reload()}>
            <RotateCcw aria-hidden />
            {t('errorPage.reload')}
          </Button>
          <Button asChild variant="secondary">
            <Link to="/" reloadDocument>
              {t('common.backHome')}
            </Link>
          </Button>
        </div>
        <details className="group w-full text-left" open={import.meta.env.DEV}>
          <summary className="flex cursor-pointer list-none items-center gap-1 text-xs text-subtle hover:text-foreground">
            <ChevronRight className="size-3 transition-transform group-open:rotate-90" aria-hidden />
            {t('errorPage.details')}
          </summary>
          <div className="mt-2 rounded-[var(--radius-control)] border border-border bg-background">
            <div className="flex items-center justify-between gap-2 border-b border-border px-3 py-1.5">
              <span className="truncate font-mono text-xs text-muted">{message}</span>
              <button
                type="button"
                onClick={copy}
                className="flex shrink-0 items-center gap-1 text-xs text-subtle hover:text-foreground"
              >
                {copied ? <Check className="size-3" aria-hidden /> : <Copy className="size-3" aria-hidden />}
                {copied ? t('common.copied') : t('common.copy')}
              </button>
            </div>
            {import.meta.env.DEV && stack ? (
              <pre className="max-h-64 overflow-auto px-3 py-2 font-mono text-[11px] leading-relaxed text-subtle">
                {stack}
              </pre>
            ) : null}
          </div>
        </details>
      </div>
    </div>
  )
}
