import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { setupMswServer } from '@/test/msw'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'

setupMswServer()
beforeEach(() => mockSession())

describe('CommandPalette', () => {
  it('AC5: opens on shortcut and focuses input', async () => {
    renderRoutes(routes, '/')
    await screen.findByRole('navigation', { name: 'Main navigation' })
    await userEvent.keyboard('{Control>}k{/Control}')
    const input = await screen.findByPlaceholderText('Type a command or search…')
    await waitFor(() => expect(input).toHaveFocus())
  })

  it('AC5: filters and navigates on Enter', async () => {
    const { router } = renderRoutes(routes, '/')
    await screen.findByRole('navigation', { name: 'Main navigation' })
    await userEvent.keyboard('{Meta>}k{/Meta}')
    const input = await screen.findByPlaceholderText('Type a command or search…')
    await userEvent.type(input, 'jobs')
    expect(screen.queryByRole('option', { name: 'Settings' })).not.toBeInTheDocument()
    await userEvent.keyboard('{Enter}')
    await waitFor(() => expect(router.state.location.pathname).toBe('/jobs'))
    expect(screen.queryByPlaceholderText('Type a command or search…')).not.toBeInTheDocument()
  })

  it('AC5: Esc closes and restores focus', async () => {
    renderRoutes(routes, '/')
    const trigger = await screen.findByRole('button', { name: /Search or jump to/ })
    await userEvent.click(trigger)
    await screen.findByPlaceholderText('Type a command or search…')
    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByPlaceholderText('Type a command or search…')).not.toBeInTheDocument())
    expect(trigger).toHaveFocus()
  })

  it('AC5: toggle theme action', async () => {
    renderRoutes(routes, '/')
    await screen.findByRole('navigation', { name: 'Main navigation' })
    await userEvent.keyboard('{Control>}k{/Control}')
    await userEvent.type(await screen.findByPlaceholderText('Type a command or search…'), 'theme')
    await userEvent.keyboard('{Enter}')
    await waitFor(() => expect(document.documentElement).toHaveClass('dark'))
  })
})
