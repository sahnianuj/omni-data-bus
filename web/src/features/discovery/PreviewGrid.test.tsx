import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { previewResult } from '@/test/discovery'
import { renderUI } from '@/test/render'
import { PreviewGrid } from './PreviewGrid'

describe('PreviewGrid', () => {
  it('AC5: renders NULL, JSON viewer, truncated expand', async () => {
    renderUI(<PreviewGrid result={previewResult()} />)
    expect(screen.getAllByRole('columnheader').map((h) => h.textContent)).toEqual(['customer_idinteger', 'preferencesany', 'notesstring'])
    expect(screen.getAllByText('NULL')).toHaveLength(2)

    // JSON cells are a collapsible viewer.
    const summary = screen.getByText('{"newsletter":true,"language":"en"}')
    expect(summary.tagName).toBe('SUMMARY')
    await userEvent.click(summary)
    expect(summary.closest('details')).toHaveAttribute('open')

    // Truncated values show 1 KB with an ellipsis; Expand shows the full value.
    expect(screen.getByText(`${'x'.repeat(1024)}…`)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Show the full value' }))
    expect(await screen.findByText('x'.repeat(1500))).toBeInTheDocument()
  })

  it('an empty table says so', () => {
    renderUI(<PreviewGrid result={previewResult({ rows: [] })} />)
    expect(screen.getByText('The table is empty.')).toBeInTheDocument()
  })
})
