import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { Button } from '@/components/ui/button'
import { setupMswServer } from '@/test/msw'
import { renderUI } from '@/test/render'
import { mockSession } from '@/test/session'
import { Can, hasRole } from './Can'

setupMswServer()

describe('Can', () => {
  it('AC6: disables children with tooltip for viewer', async () => {
    mockSession('viewer')
    renderUI(
      <Can role="editor">
        <Button>New project</Button>
      </Can>,
    )
    await screen.findByRole('button', { name: 'New project' })
    await waitFor(() => expect(screen.getByRole('button', { name: 'New project' })).toBeDisabled())
    await userEvent.hover(screen.getByRole('button', { name: 'New project' }).parentElement!)
    expect(await screen.findAllByText('Viewer role — read only')).not.toHaveLength(0)
  })

  it('AC6: enables children for editors and owners', async () => {
    mockSession('editor')
    renderUI(
      <Can role="editor">
        <Button>New project</Button>
      </Can>,
    )
    await waitFor(() => expect(screen.getByRole('button', { name: 'New project' })).toBeEnabled())
  })

  it('hasRole ranks roles', () => {
    expect(hasRole('owner', 'editor')).toBe(true)
    expect(hasRole('editor', 'editor')).toBe(true)
    expect(hasRole('viewer', 'editor')).toBe(false)
    expect(hasRole(undefined, 'viewer')).toBe(false)
  })
})
