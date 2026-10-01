import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { scol } from '@/test/mapping'
import { FilterChips } from './FilterChips'
import type { RelationFilter } from './types'

function Harness({ onChange }: { onChange: (f: RelationFilter[]) => void }) {
  const [value, setValue] = useState<RelationFilter[]>([])
  return (
    <FilterChips
      columns={[scol('status', 'string'), scol('quantity', 'integer'), scol('placed_on', 'datetime', { udtName: 'date' }), { ...scol('qty', 'integer'), from: 'via' }]}
      value={value}
      onChange={(v) => {
        setValue(v)
        onChange(v)
      }}
    />
  )
}

describe('FilterChips', () => {
  it('AC5: typed values per column', async () => {
    let last: RelationFilter[] = []
    render(<Harness onChange={(v) => (last = v)} />)
    await userEvent.selectOptions(screen.getByLabelText('Filter column'), 'status')
    expect(screen.getByLabelText('Filter value')).toHaveAttribute('type', 'text')
    await userEvent.type(screen.getByLabelText('Filter value'), 'active')
    await userEvent.click(screen.getByRole('button', { name: 'Add' }))
    expect(last).toEqual([{ column: 'status', op: 'eq', value: 'active' }])

    // A number column takes a number and sends a number.
    await userEvent.selectOptions(screen.getByLabelText('Filter column'), 'quantity')
    expect(screen.getByLabelText('Filter value')).toHaveAttribute('type', 'number')
    await userEvent.selectOptions(screen.getByLabelText('Operator'), 'gt')
    await userEvent.type(screen.getByLabelText('Filter value'), '0')
    await userEvent.click(screen.getByRole('button', { name: 'Add' }))
    expect(last[1]).toEqual({ column: 'quantity', op: 'gt', value: 0 })

    // A date column gets a date input; in takes a list; a junction column is marked from via.
    await userEvent.selectOptions(screen.getByLabelText('Filter column'), 'placed_on')
    expect(screen.getByLabelText('Filter value')).toHaveAttribute('type', 'date')
    await userEvent.selectOptions(screen.getByLabelText('Filter column'), 'via:qty')
    await userEvent.selectOptions(screen.getByLabelText('Operator'), 'in')
    await userEvent.type(screen.getByLabelText('Filter value'), '1, 2')
    await userEvent.click(screen.getByRole('button', { name: 'Add' }))
    expect(last[2]).toEqual({ column: 'qty', op: 'in', value: [1, 2], from: 'via' })

    // isNull needs no value; chips can be removed.
    await userEvent.selectOptions(screen.getByLabelText('Operator'), 'isNull')
    expect(screen.queryByLabelText('Filter value')).not.toBeInTheDocument()
    expect(screen.getAllByRole('listitem')).toHaveLength(3)
    await userEvent.click(screen.getByRole('button', { name: 'Remove the filter on status' }))
    expect(last.map((f) => f.column)).toEqual(['quantity', 'qty'])
  })
})
