import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { renderUI } from '@/test/render'
import { AdvancedOptions } from './AdvancedOptions'
import type { JobOptions } from './api'

describe('JobOptions', () => {
  it('AC5: hides unsupported options', async () => {
    const onChange = vi.fn<(o: JobOptions | null) => void>()
    const { unmount } = renderUI(<AdvancedOptions onChange={onChange} />)
    await userEvent.click(screen.getByRole('button', { name: /Advanced options/ }))
    expect(screen.getByLabelText('Durability')).toBeInTheDocument()
    unmount()

    // A directory target has no durability levels: the option is hidden and the job asks for none.
    renderUI(<AdvancedOptions onChange={onChange} capabilities={{ supportsDurability: false }} />)
    await userEvent.click(screen.getByRole('button', { name: /Advanced options/ }))
    expect(screen.queryByLabelText('Durability')).not.toBeInTheDocument()
    expect(screen.getByLabelText('Write mode')).toBeInTheDocument()
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ durability: 'none' }))
  })
})
