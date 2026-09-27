import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { installMatchMedia } from '@/test/matchMedia'
import { THEME_KEY, ThemeProvider, useTheme } from './ThemeProvider'

function Probe() {
  const { preference, resolved, setPreference } = useTheme()
  return (
    <div>
      <span data-testid="state">{`${preference}/${resolved}`}</span>
      <button onClick={() => setPreference('dark')}>dark</button>
      <button onClick={() => setPreference('system')}>system</button>
    </div>
  )
}

describe('ThemeProvider', () => {
  it('AC3: applies stored preference', () => {
    window.localStorage.setItem(THEME_KEY, 'dark')
    render(<ThemeProvider><Probe /></ThemeProvider>)
    expect(screen.getByTestId('state')).toHaveTextContent('dark/dark')
    expect(document.documentElement).toHaveClass('dark')
  })

  it('AC3: persists a new preference', async () => {
    render(<ThemeProvider><Probe /></ThemeProvider>)
    await userEvent.click(screen.getByText('dark'))
    expect(window.localStorage.getItem(THEME_KEY)).toBe('dark')
    expect(document.documentElement).toHaveClass('dark')
  })

  it('AC3: follows matchMedia when system', async () => {
    const mm = installMatchMedia(false)
    render(<ThemeProvider><Probe /></ThemeProvider>)
    expect(screen.getByTestId('state')).toHaveTextContent('system/light')
    act(() => mm.setDark(true))
    expect(screen.getByTestId('state')).toHaveTextContent('system/dark')
    expect(document.documentElement).toHaveClass('dark')
    act(() => mm.setDark(false))
    expect(document.documentElement).not.toHaveClass('dark')
  })

  it('AC3: survives unavailable storage', () => {
    const orig = Object.getOwnPropertyDescriptor(window, 'localStorage')!
    Object.defineProperty(window, 'localStorage', { get: () => { throw new Error('blocked') }, configurable: true })
    try {
      render(<ThemeProvider><Probe /></ThemeProvider>)
      expect(screen.getByTestId('state')).toHaveTextContent('system/light')
    } finally {
      Object.defineProperty(window, 'localStorage', orig)
    }
  })
})
