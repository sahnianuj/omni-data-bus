import { Activity, FolderKanban, LayoutGrid, Settings, type LucideIcon } from 'lucide-react'

export type NavItem = { to: string; labelKey: string; icon: LucideIcon; end?: boolean }

export const navItems: NavItem[] = [
  { to: '/', labelKey: 'nav.dashboard', icon: LayoutGrid, end: true },
  { to: '/projects', labelKey: 'nav.projects', icon: FolderKanban },
  { to: '/jobs', labelKey: 'nav.jobs', icon: Activity },
  { to: '/settings', labelKey: 'nav.settings', icon: Settings },
]
