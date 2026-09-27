import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { failedTone, StatTile } from './StatTile'

describe('StatTile', () => {
  it('AC5: failed tile thresholds', () => {
    expect(failedTone(0, 1000)).toBe('neutral')
    expect(failedTone(123, 1000)).toBe('warning')
    expect(failedTone(501, 1000)).toBe('danger')
    expect(failedTone(5000, -1)).toBe('warning') // no absolute limit: never red
    render(<StatTile label="Failed" value="123" tone={failedTone(123, 1000)} sub="0.03% · view errors →" />)
    const tile = screen.getByRole('region', { name: 'Failed' })
    expect(tile).toHaveAttribute('data-tone', 'warning')
    expect(tile).toHaveTextContent('123')
    expect(tile).toHaveTextContent('0.03% · view errors →')
  })
})
