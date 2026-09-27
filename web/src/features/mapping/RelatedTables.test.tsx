import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { setupMswServer } from '@/test/msw'
import { embedView, renderWorkbench } from '@/test/mapping'
import { mockSession } from '@/test/session'
import { RelatedTables } from './RelatedTables'

setupMswServer()
beforeEach(() => mockSession('owner'))

describe('RelatedTables', () => {
  it('AC1: lists parents and children with counts', async () => {
    renderWorkbench(<RelatedTables />, embedView())
    const list = screen.getByRole('region', { name: 'Related tables' })
    const items = within(list).getAllByRole('listitem')
    expect(items.map((i) => i.textContent)).toEqual([
      expect.stringContaining('↗ address via address_id'),
      expect.stringContaining('↙ orders via customer_id'),
      expect.stringContaining('↙ page_view via customer_id'),
    ])
    expect(items[0]).toHaveTextContent('~400 rows')
    expect(items[1]).toHaveTextContent('~600 per row')
    expect(within(items[1]!).getByText('Embedded')).toBeInTheDocument()
    // More child rows per parent than the default limit: a warning with the numbers.
    expect(within(items[2]!).getByLabelText('About 2,500 rows per document, more than the limit of 1000: arrays will be truncated.')).toBeInTheDocument()
    expect(await within(items[0]!).findByRole('button', { name: 'Embed' })).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: /Related tables/ }))
    expect(within(list).queryAllByRole('listitem')).toHaveLength(0)
  })

  it('says when there are no related tables', () => {
    renderWorkbench(<RelatedTables />, embedView({ embedCandidates: [] }))
    expect(screen.getByText('No foreign keys to or from this table.')).toBeInTheDocument()
  })
})
