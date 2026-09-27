import * as DialogPrimitive from '@radix-ui/react-dialog'
import { Command } from 'cmdk'
import { Moon, Plus } from 'lucide-react'
import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router'
import { navItems } from '@/app/nav'
import { useTheme } from '@/app/ThemeProvider'

const itemClass =
  'flex cursor-pointer items-center gap-3 rounded-md px-3 py-2 text-sm text-foreground data-[selected=true]:bg-primary-soft data-[selected=true]:text-primary-soft-foreground [&_svg]:size-4 [&_svg]:text-subtle'

/** ⌘K / Ctrl+K palette for navigation and actions (OC-004 AC5). */
export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { resolved, setPreference } = useTheme()
  // Focus returns to whatever opened the palette (button or keyboard shortcut).
  const opener = useRef<HTMLElement | null>(null)
  useEffect(() => {
    if (open) opener.current = document.activeElement as HTMLElement | null
  }, [open])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() === 'k' && (e.metaKey || e.ctrlKey)) {
        e.preventDefault()
        onOpenChange(!open)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onOpenChange])

  const run = (fn: () => void) => {
    onOpenChange(false)
    fn()
  }

  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/40" />
        <DialogPrimitive.Content
          aria-describedby={undefined}
          onCloseAutoFocus={(e) => {
            e.preventDefault()
            opener.current?.focus()
          }}
          className="fixed left-1/2 top-[18%] z-50 w-full max-w-xl -translate-x-1/2 overflow-hidden rounded-[var(--radius-dialog)] border border-border bg-surface shadow-lg"
        >
          <DialogPrimitive.Title className="sr-only">{t('search.title')}</DialogPrimitive.Title>
          <Command label={t('search.title')}>
            <Command.Input
              autoFocus
              placeholder={t('search.placeholder')}
              className="h-12 w-full border-b border-border bg-transparent px-4 text-sm outline-none placeholder:text-subtle"
            />
            <Command.List className="max-h-80 overflow-y-auto p-2">
              <Command.Empty className="px-3 py-6 text-center text-sm text-muted">{t('search.empty')}</Command.Empty>
              <Command.Group heading={t('search.navigation')} className="text-xs text-subtle [&_[cmdk-group-heading]]:px-3 [&_[cmdk-group-heading]]:py-1.5">
                {navItems.map((item) => (
                  <Command.Item key={item.to} value={t(item.labelKey)} onSelect={() => run(() => navigate(item.to))} className={itemClass}>
                    <item.icon aria-hidden />
                    {t(item.labelKey)}
                  </Command.Item>
                ))}
              </Command.Group>
              <Command.Group heading={t('search.actions')} className="text-xs text-subtle [&_[cmdk-group-heading]]:px-3 [&_[cmdk-group-heading]]:py-1.5">
                <Command.Item value={t('search.newProject')} onSelect={() => run(() => navigate('/projects?new=1'))} className={itemClass}>
                  <Plus aria-hidden />
                  {t('search.newProject')}
                </Command.Item>
                <Command.Item
                  value={t('search.toggleTheme')}
                  onSelect={() => run(() => setPreference(resolved === 'dark' ? 'light' : 'dark'))}
                  className={itemClass}
                >
                  <Moon aria-hidden />
                  {t('search.toggleTheme')}
                </Command.Item>
              </Command.Group>
            </Command.List>
          </Command>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}
