import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { ConfirmDialog } from './ConfirmDialog'

describe('ConfirmDialog', () => {
  it('AC7: enables confirm only on exact name', async () => {
    const onConfirm = vi.fn()
    render(
      <ConfirmDialog open onOpenChange={() => {}} title="Delete project" confirmLabel="Delete" destructive requireTypedName="prod-pg" onConfirm={onConfirm} />,
    )
    const confirm = screen.getByRole('button', { name: 'Delete' })
    expect(confirm).toBeDisabled()
    const input = screen.getByRole('textbox')
    await userEvent.type(input, 'prod-p')
    expect(confirm).toBeDisabled()
    await userEvent.type(input, 'G')
    expect(confirm).toBeDisabled()
    await userEvent.clear(input)
    await userEvent.type(input, 'prod-pg')
    expect(confirm).toBeEnabled()
    await userEvent.click(confirm)
    expect(onConfirm).toHaveBeenCalledOnce()
  })

  it('AC7: cancel closes without confirming', async () => {
    const onOpenChange = vi.fn()
    const onConfirm = vi.fn()
    render(<ConfirmDialog open onOpenChange={onOpenChange} title="Sure?" confirmLabel="Yes" onConfirm={onConfirm} />)
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
    expect(onConfirm).not.toHaveBeenCalled()
  })
})
