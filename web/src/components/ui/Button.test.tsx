import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { Button } from './button'

describe('Button', () => {
  it('AC6: loading state sets aria-busy and disables', async () => {
    const onClick = vi.fn()
    render(<Button loading onClick={onClick}>Save</Button>)
    const btn = screen.getByRole('button', { name: 'Save' })
    expect(btn).toHaveAttribute('aria-busy', 'true')
    expect(btn).toBeDisabled()
    await userEvent.click(btn)
    expect(onClick).not.toHaveBeenCalled()
  })

  it('AC6: defaults to type=button so it never submits forms by accident', () => {
    render(<Button>Go</Button>)
    expect(screen.getByRole('button')).toHaveAttribute('type', 'button')
  })

  it('asChild renders the child element (e.g. a link) as the button', () => {
    render(
      <Button asChild>
        <a href="/projects">Projects</a>
      </Button>,
    )
    expect(screen.getByRole('link', { name: 'Projects' })).toHaveAttribute('href', '/projects')
  })
})
