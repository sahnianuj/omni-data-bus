import { render as rtlRender, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { ReactElement } from 'react'
import { TooltipProvider } from '@/components/ui/tooltip'
import '@/i18n'
import { AdvancedOptions, optionsSchema } from './AdvancedOptions'
import { defaultJobOptions } from './api'

// The app provides tooltips from its root (app/providers.tsx).
const render = (ui: ReactElement) => rtlRender(<TooltipProvider>{ui}</TooltipProvider>)

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
    expect(ok({ readParallelism: '8' })).toBe(true)
    expect(ok({ readParallelism: '9' })).toBe(false)
    expect(ok({ tableConcurrency: 6 })).toBe(true)
    expect(ok({ tableConcurrency: 7 })).toBe(false)
    expect(ok({ maxDocsPerSecond: 0 })).toBe(false)
    expect(ok({ errorPolicy: { maxErrors: -1, maxErrorRatio: 0.5, minRowsForRatio: 0 } })).toBe(true)
    expect(ok({ errorPolicy: { maxErrors: -2, maxErrorRatio: 0.5, minRowsForRatio: 0 } })).toBe(false)
    expect(ok({ durability: 'eventually' })).toBe(false)
  })

  it('explains every option in an ⓘ tooltip on hover and keyboard focus', async () => {
    render(<AdvancedOptions onChange={() => {}} />)
    await userEvent.click(screen.getByRole('button', { name: 'Advanced options' }))
    const options = ['Write mode', 'Batch size', 'Read parallelism', 'Write concurrency', 'Tables in parallel', 'Max docs per second', 'Durability', 'Stop after errors', 'Stop at error ratio', 'Exact row count']
    for (const o of options) expect(screen.getByRole('button', { name: `About ${o}` })).toBeInTheDocument()

    await userEvent.hover(screen.getByRole('button', { name: 'About Tables in parallel' }))
    expect((await screen.findAllByText(/How many mappings \(tables\) migrate at the same time/)).length).toBeGreaterThan(0)
    await userEvent.unhover(screen.getByRole('button', { name: 'About Tables in parallel' }))

    // Keyboard users get the same text on focus.
    screen.getByRole('button', { name: 'About Durability' }).focus()
    expect((await screen.findAllByText(/Persist to majority: on disk on a majority of nodes/)).length).toBeGreaterThan(0)

    // Clicking the label (not the ⓘ) still focuses its control.
    await userEvent.click(screen.getByText('Batch size', { selector: 'label' }))
    expect(screen.getByRole('spinbutton', { name: 'Batch size' })).toHaveFocus()
  })
})
