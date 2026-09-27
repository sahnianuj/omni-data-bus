import { PanelLeftClose, PanelLeftOpen, Search } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { NavLink, Outlet } from 'react-router'
import { navItems } from '@/app/nav'
import { storage } from '@/lib/storage'
import { cn } from '@/lib/utils'
import { Breadcrumbs } from './Breadcrumbs'
import { CommandPalette } from './CommandPalette'
import { Logo } from './Logo'
import { ThemeMenu } from './ThemeMenu'
import { Kbd } from './ui/kbd'
import { Tooltip } from './ui/tooltip'

export const SIDEBAR_KEY = 'oc-sidebar-collapsed'

/** Sidebar + top bar layout for every signed-in page (OC-004 AC1). */
export function AppShell({ userSlot, workspaceName }: { userSlot?: ReactNode; workspaceName?: string }) {
  const { t } = useTranslation()
  const [collapsed, setCollapsed] = useState(() => storage.get(SIDEBAR_KEY) === 'true')
  const [paletteOpen, setPaletteOpen] = useState(false)

  const toggle = () => {
    const next = !collapsed
    storage.set(SIDEBAR_KEY, String(next))
    setCollapsed(next)
  }

  return (
    <div className="flex h-screen overflow-hidden bg-background">
      <a href="#main" className="sr-only focus:not-sr-only focus:absolute focus:left-3 focus:top-3 focus:z-50 focus:rounded-md focus:bg-surface focus:px-3 focus:py-2">
        {t('nav.skipToContent')}
      </a>
      <aside
        data-collapsed={collapsed}
        className={cn('flex shrink-0 flex-col gap-1 border-r border-border bg-surface px-3 py-4 transition-[width]', collapsed ? 'w-16 items-center' : 'w-60')}
      >
        <div className={cn('mb-3 flex items-center gap-2.5 px-2', collapsed && 'px-0')}>
          <Logo size={28} />
          {collapsed ? null : <span className="text-[15px] font-semibold tracking-tight">{t('app.name')}</span>}
        </div>
        {collapsed || !workspaceName ? null : (
          <div className="mb-3 truncate rounded-md border border-border px-2.5 py-2 text-[13px] text-muted">{workspaceName}</div>
        )}
        <nav aria-label={t('nav.main')} className="flex flex-col gap-1">
          {navItems.map((item) => {
            const link = (
              <NavLink
                key={item.to}
                to={item.to}
                end={item.end}
                aria-label={collapsed ? t(item.labelKey) : undefined}
                className={({ isActive }) =>
                  cn(
                    'flex h-9 items-center gap-2.5 rounded-md px-2.5 text-sm text-muted hover:bg-surface-muted hover:text-foreground [&_svg]:size-4',
                    collapsed && 'w-10 justify-center px-0',
                    isActive && 'bg-primary-soft font-medium text-primary-soft-foreground hover:bg-primary-soft hover:text-primary-soft-foreground',
                  )
                }
              >
                <item.icon aria-hidden />
                {collapsed ? null : t(item.labelKey)}
              </NavLink>
            )
            return collapsed ? (
              <Tooltip key={item.to} content={t(item.labelKey)}>
                {link}
              </Tooltip>
            ) : (
              link
            )
          })}
        </nav>
        <div className="flex-1" />
        {collapsed ? null : <div className="px-1 pb-2">{userSlot}</div>}
        <div className={cn('flex items-center justify-between gap-2 border-t border-border pt-3', collapsed && 'flex-col')}>
          {collapsed ? null : <ThemeMenu />}
          <button
            type="button"
            onClick={toggle}
            aria-label={collapsed ? t('nav.expand') : t('nav.collapse')}
            aria-expanded={!collapsed}
            className="flex size-8 items-center justify-center rounded-md text-subtle hover:bg-surface-muted hover:text-foreground"
          >
            {collapsed ? <PanelLeftOpen className="size-4" aria-hidden /> : <PanelLeftClose className="size-4" aria-hidden />}
          </button>
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 shrink-0 items-center justify-between border-b border-border bg-surface px-8">
          <Breadcrumbs />
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => setPaletteOpen(true)}
              className="flex h-8 w-72 items-center gap-2 rounded-md border border-border bg-background px-2.5 text-[13px] text-subtle hover:border-border-strong"
            >
              <Search className="size-3.5" aria-hidden />
              <span className="flex-1 text-left">{t('search.trigger')}</span>
              <Kbd>{t('search.shortcut')}</Kbd>
            </button>
          </div>
        </header>
        <main id="main" className="flex-1 overflow-y-auto">
          <Outlet />
        </main>
      </div>
      <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
    </div>
  )
}
