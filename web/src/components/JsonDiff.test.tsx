import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { renderUI } from '@/test/render'
import { JsonDiff } from './JsonDiff'

describe('JsonDiff', () => {
  it('AC5: highlights differing paths', async () => {
    const { container } = renderUI(
      <JsonDiff
        expected={{ id: 3, total: 12.5, lines: [{ sku: 'a' }, { sku: 'b' }] }}
        actual={{ id: 3, total: 12, lines: [{ sku: 'a' }, { sku: 'x' }] }}
        paths={['$.total', '$.lines[1]']}
      />,
    )
    expect(await screen.findByLabelText('Expected (from source)')).toHaveTextContent('"total": 12.5')
    expect(screen.getByLabelText('Actual (in Couchbase)')).toHaveTextContent('"total": 12')
    const hit = [...container.querySelectorAll('[data-diff]')].map((e) => e.getAttribute('data-path'))
    // Both panes: $.total, and $.lines[1] with everything inside it.
    expect(hit).toEqual(expect.arrayContaining(['$.total', '$.lines[1]', '$.lines[1].sku']))
    expect(hit).not.toContain('$.id')
    expect(hit).not.toContain('$.lines[0].sku')
    expect(hit.filter((p) => p === '$.total')).toHaveLength(2)
  })
})
