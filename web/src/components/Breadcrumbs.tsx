import type { TFunction } from 'i18next'
import { Fragment } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useMatches } from 'react-router'

/** Route handle: routes declare `handle: { crumb: (t) => 'Projects' }`. */
export type CrumbHandle = { crumb?: (t: TFunction) => string }

export function Breadcrumbs() {
  const { t } = useTranslation()
  const crumbs = useMatches()
    .map((m) => ({ path: m.pathname, crumb: (m.handle as CrumbHandle | undefined)?.crumb }))
    .filter((c): c is { path: string; crumb: (t: TFunction) => string } => typeof c.crumb === 'function')
  return (
    <nav aria-label={t('nav.breadcrumb')} className="flex items-center gap-2 text-[13px] text-subtle">
      {crumbs.map((c, i) => (
        <Fragment key={c.path}>
          {i > 0 ? <span aria-hidden>/</span> : null}
          {i === crumbs.length - 1 ? (
            <span aria-current="page" className="text-foreground">
              {c.crumb(t)}
            </span>
          ) : (
            <Link to={c.path} className="hover:text-foreground">
              {c.crumb(t)}
            </Link>
          )}
        </Fragment>
      ))}
    </nav>
  )
}
