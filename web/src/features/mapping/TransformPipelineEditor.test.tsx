import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import '@/i18n'
import { TransformPipelineEditor } from './TransformPipelineEditor'
import type { TransformStep } from './types'

/** Keeps the steps in state like the Inspector does, and reports each change. */
function Harness({ initial, suggestions, onSteps }: { initial: TransformStep[]; suggestions?: TransformStep[]; onSteps: (s: TransformStep[]) => void }) {
  const [steps, setSteps] = useState(initial)
  return (
    <TransformPipelineEditor
      steps={steps}
      suggestions={suggestions}
      onChange={(s) => {
        onSteps(s)
        setSteps(s)
      }}
    />
  )
}

const ops = () => within(screen.getByRole('list', { name: 'Transform steps' })).getAllByRole('listitem').map((li) => li.getAttribute('aria-label'))

describe('TransformPipelineEditor', () => {
  it('AC8: add/reorder/remove steps', async () => {
    const seen = { steps: [] as TransformStep[] }
    render(<Harness initial={[{ op: 'trim' }]} onSteps={(s) => (seen.steps = s)} />)

    await userEvent.click(screen.getByRole('button', { name: 'Add transform' }))
    await userEvent.type(screen.getByRole('textbox', { name: 'Search transforms' }), 'upper')
    await userEvent.click(screen.getByRole('button', { name: 'Uppercase' }))
    expect(ops()).toEqual(['Step 1: Trim', 'Step 2: Uppercase'])

    const second = screen.getByRole('listitem', { name: 'Step 2: Uppercase' })
    await userEvent.click(within(second).getByRole('button', { name: 'Move up' }))
    expect(ops()).toEqual(['Step 1: Uppercase', 'Step 2: Trim'])
    expect(within(screen.getByRole('listitem', { name: 'Step 1: Uppercase' })).getByRole('button', { name: 'Move up' })).toBeDisabled()

    await userEvent.click(within(screen.getByRole('listitem', { name: 'Step 2: Trim' })).getByRole('button', { name: 'Remove step' }))
    expect(seen.steps).toEqual([{ op: 'upper' }])
  })

  it('AC8: configures args and shows inline errors', async () => {
    const seen = { steps: [] as TransformStep[] }
    render(<Harness initial={[{ op: 'formatDatetime', args: { format: 'custom' } }]} onSteps={(s) => (seen.steps = s)} />)
    // A custom format needs a pattern (zod refinement).
    expect(screen.getByText(/pattern/i, { selector: '.text-danger' })).toBeInTheDocument()
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Format' }), 'epoch_ms')
    expect(seen.steps).toEqual([{ op: 'formatDatetime', args: { format: 'epoch_ms' } }])
    expect(screen.queryByText(/pattern/i, { selector: '.text-danger' })).not.toBeInTheDocument()
  })

  it('AC8: suggestion chip adds step', async () => {
    const seen = { steps: [] as TransformStep[] }
    render(<Harness initial={[{ op: 'trim' }]} suggestions={[{ op: 'formatDatetime', args: { format: 'epoch_ms' } }]} onSteps={(s) => (seen.steps = s)} />)
    await userEvent.click(screen.getByRole('button', { name: 'Add Format datetime' }))
    expect(seen.steps).toEqual([{ op: 'formatDatetime', args: { format: 'epoch_ms' } }, { op: 'trim' }])
    expect(ops()).toEqual(['Step 1: Format datetime', 'Step 2: Trim'])
  })
})
