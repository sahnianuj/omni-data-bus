import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { SecretInput } from './SecretInput'

function Harness({ stored }: { stored: boolean }) {
  const [value, setValue] = useState<string | undefined>(undefined)
  return (
    <>
      <SecretInput id="pw" stored={stored} value={value} onChange={setValue} />
      <output data-testid="value">{value === undefined ? 'KEEP' : `SET:${value}`}</output>
    </>
  )
}

describe('SecretInput', () => {
  it('AC5: shows stored placeholder and change toggles input', async () => {
    render(<Harness stored />)
    const field = screen.getByRole('textbox')
    expect(field).toHaveValue('•••••••• (stored)')
    expect(field).toBeDisabled()
    expect(screen.getByTestId('value')).toHaveTextContent('KEEP')

    await userEvent.click(screen.getByRole('button', { name: 'Change' }))
    const pw = document.getElementById('pw') as HTMLInputElement
    expect(pw).toHaveAttribute('type', 'password')
    expect(pw).toHaveAttribute('autocomplete', 'new-password')
    expect(pw).toHaveValue('')
    await userEvent.type(pw, 's3cret')
    expect(screen.getByTestId('value')).toHaveTextContent('SET:s3cret')

    await userEvent.click(screen.getByRole('button', { name: 'Keep stored' }))
    expect(screen.getByTestId('value')).toHaveTextContent('KEEP')
  })

  it('AC5: new connection shows an empty password field with show/hide', async () => {
    render(<Harness stored={false} />)
    const pw = document.getElementById('pw') as HTMLInputElement
    expect(pw).toHaveValue('')
    await userEvent.type(pw, 'abc')
    await userEvent.click(screen.getByRole('button', { name: 'Show' }))
    expect(pw).toHaveAttribute('type', 'text')
    expect(screen.queryByRole('button', { name: 'Keep stored' })).not.toBeInTheDocument()
  })
})
