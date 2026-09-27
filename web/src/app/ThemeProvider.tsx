import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { storage } from '@/lib/storage'

export type ThemePreference = 'light' | 'dark' | 'system'
export const THEME_KEY = 'oc-theme'

type ThemeContextValue = {
  preference: ThemePreference
  resolved: 'light' | 'dark'
  setPreference: (p: ThemePreference) => void
}

const ThemeContext = createContext<ThemeContextValue | null>(null)

function systemDark() {
  return typeof window.matchMedia === 'function' && window.matchMedia('(prefers-color-scheme: dark)').matches
}

function readPreference(): ThemePreference {
  const v = storage.get(THEME_KEY)
  return v === 'light' || v === 'dark' || v === 'system' ? v : 'system'
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [preference, setPref] = useState<ThemePreference>(readPreference)
  const [osDark, setOsDark] = useState(systemDark)

  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const onChange = (e: MediaQueryListEvent) => setOsDark(e.matches)
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [])

  const resolved: 'light' | 'dark' = preference === 'system' ? (osDark ? 'dark' : 'light') : preference

  useEffect(() => {
    document.documentElement.classList.toggle('dark', resolved === 'dark')
  }, [resolved])

  const setPreference = useCallback((p: ThemePreference) => {
    storage.set(THEME_KEY, p)
    setPref(p)
  }, [])

  const value = useMemo(() => ({ preference, resolved, setPreference }), [preference, resolved, setPreference])
  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}

export function useTheme() {
  const ctx = useContext(ThemeContext)
  if (!ctx) throw new Error('useTheme must be used inside ThemeProvider')
  return ctx
}
