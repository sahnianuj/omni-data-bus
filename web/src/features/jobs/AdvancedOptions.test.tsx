import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import '@/i18n'
import { AdvancedOptions, optionsSchema } from './AdvancedOptions'
import { defaultJobOptions } from './api'

describe('AdvancedOptions', () => {
  it('AC3: form validation mirrors server', async () => {
    const onChange = vi.fn()
    render(<AdvancedOptions onChange={onChange} />)
    expect(onChange).toHaveBeenLastCalledWith(defaultJobOptions)
    await userEvent.click(screen.getByRole('button', { name: 'Advanced options' }))

    const batch = screen.getByRole('spinbutton', { name: 'Batch size' })
    await userEvent.clear(batch)
    await userEvent.type(batch, '50')
    expect(await screen.findByText('At least 100')).toBeInTheDocument()
    expect(onChange).toHaveBeenLastCalledWith(null)
    expect(screen.getByText('Fix the highlighted options')).toBeInTheDocument()

    const conc = screen.getByRole('spinbutton', { name: 'Write concurrency' })
    await userEvent.clear(conc)
    await userEvent.type(conc, '1000')
    expect(await screen.findByText('At most 512')).toBeInTheDocument()

    await userEvent.clear(batch)
    await userEvent.type(batch, '2000')
    await userEvent.clear(conc)
    await userEvent.type(conc, '128')
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Write mode' }), 'insert')
    expect(onChange).toHaveBeenLastCalledWith({ ...defaultJobOptions, batchSize: 2000, writeConcurrency: 128, writeMode: 'insert' })
  })

  it('schema matches the server limits', () => {
    const ok = (patch: object) => optionsSchema.safeParse({ ...defaultJobOptions, ...patch }).success
    expect(ok({})).toBe(true)
    expect(ok({ batchSize: 99 })).toBe(false)
    expect(ok({ batchSize: 10001 })).toBe(false)
    expect(ok({ readParallelism: '16' })).toBe(true)
    expect(ok({ readParallelism: '17' })).toBe(false)
    expect(ok({ tableConcurrency: 5 })).toBe(false)
    expect(ok({ maxDocsPerSecond: 0 })).toBe(false)
    expect(ok({ errorPolicy: { maxErrors: -1, maxErrorRatio: 0.5, minRowsForRatio: 0 } })).toBe(true)
    expect(ok({ errorPolicy: { maxErrors: -2, maxErrorRatio: 0.5, minRowsForRatio: 0 } })).toBe(false)
    expect(ok({ durability: 'eventually' })).toBe(false)
  })
})
