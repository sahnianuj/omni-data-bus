import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { columns } from '@/test/mapping'
import { renderUI } from '@/test/render'
import { KeyTemplateInput } from './KeyTemplateInput'

function Harness({ initial = '' }: { initial?: string }) {
  const [v, setV] = useState(initial)
  return (
    <>
      <KeyTemplateInput value={v} onChange={setV} columns={columns} />
      <output data-testid="value">{v}</output>
    </>
  )
}

describe('KeyTemplateInput', () => {
  it('AC6: autocomplete inserts token', async () => {
    renderUI(<Harness />)
    const input = screen.getByRole('combobox', { name: 'Key template' })
    await userEvent.type(input, 'customer::{{cust')
    expect(input).toHaveAttribute('aria-expanded', 'true')
    const options = screen.getAllByRole('option')
    expect(options[0]).toHaveTextContent('customer_id')
    await userEvent.click(options[0]!)
    expect(screen.getByTestId('value')).toHaveTextContent('customer::{customer_id}')
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  })

  it('AC6: keyboard selection with arrows and Enter', async () => {
    renderUI(<Harness />)
    const input = screen.getByRole('combobox', { name: 'Key template' })
    await userEvent.type(input, 'c::{{_name')
    expect(screen.getAllByRole('option').map((o) => o.textContent)).toEqual([expect.stringContaining('first_name'), expect.stringContaining('last_name')])
    await userEvent.keyboard('{ArrowDown}{Enter}')
    expect(screen.getByTestId('value')).toHaveTextContent('c::{last_name}')
  })
})
