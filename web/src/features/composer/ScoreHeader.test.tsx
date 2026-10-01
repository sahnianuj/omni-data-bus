import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { MappingStats } from '@/features/mapping/types'
import { renderUI } from '@/test/render'
import { ScoreHeader } from './ScoreHeader'

// The server's automapStats.byNode for a composed order (mapping.ByNode).
const byNode: NonNullable<MappingStats['byNode']> = [
  { nodeId: 'fld_root', path: 'root', mapped: 8, total: 10 },
  { nodeId: 'f_items', relationId: 'rel_items', path: 'items', mapped: 5, total: 6 },
  { nodeId: 'f_product', relationId: 'rel_prod', path: 'items.product', mapped: 3, total: 3 },
]

describe('ScoreHeader', () => {
  it('AC5: per-node breakdown', async () => {
    const onAll = vi.fn()
    const onSelect = vi.fn()
    renderUI(<ScoreHeader byNode={byNode} onAutoMapAll={onAll} onSelect={onSelect} />)
    expect(screen.getByTestId('score-total')).toHaveTextContent('16 of 19 fields mapped')
    const list = screen.getByRole('list', { name: 'Mapped fields per bound scope' })
    expect(list).toHaveTextContent('root 8/10·items 5/6·items.product 3/3')
    await userEvent.click(screen.getByRole('button', { name: 'items.product: 3 of 3 mapped' }))
    expect(onSelect).toHaveBeenCalledWith('f_product')
    await userEvent.click(screen.getByRole('button', { name: 'Auto-map all' }))
    expect(onAll).toHaveBeenCalled()
  })
})
