import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { setupMswServer } from '@/test/msw'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'
import { SIDEBAR_KEY } from './AppShell'

setupMswServer()
beforeEach(() => mockSession())

describe('AppShell', () => {
  it('AC1: renders nav items and marks active route', async () => {
    renderRoutes(routes, '/projects')
    const nav = await screen.findByRole('navigation', { name: 'Main navigation' })
    for (const label of ['Dashboard', 'Projects', 'Jobs', 'Settings']) {
      expect(nav).toHaveTextContent(label)
    }
    expect(screen.getByRole('link', { name: 'Projects' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: 'Dashboard' })).not.toHaveAttribute('aria-current')
  })

  it('AC1: persists collapsed state', async () => {
    const { unmount } = renderRoutes(routes, '/')
    await userEvent.click(await screen.findByRole('button', { name: 'Collapse sidebar' }))
    expect(window.localStorage.getItem(SIDEBAR_KEY)).toBe('true')
    unmount()
    renderRoutes(routes, '/')
    expect(await screen.findByRole('button', { name: 'Expand sidebar' })).toBeInTheDocument()
    expect(screen.queryByText('Omni-Data-Bus')).not.toBeInTheDocument()
  })

  it('AC1: skip link targets main content', async () => {
    renderRoutes(routes, '/')
    expect(await screen.findByRole('link', { name: 'Skip to content' })).toHaveAttribute('href', '#main')
    expect(document.getElementById('main')).toBeInTheDocument()
  })

  it('AC1: shows the signed-in user and workspace', async () => {
    renderRoutes(routes, '/')
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()
    expect(screen.getByText('Owner')).toBeInTheDocument()
    expect(screen.getByText("Ada Lovelace's workspace")).toBeInTheDocument()
  })
})
