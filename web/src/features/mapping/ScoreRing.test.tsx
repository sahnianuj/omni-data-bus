import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { renderUI } from '@/test/render'
import { ScoreRing } from './ScoreRing'

describe('ScoreRing', () => {
  it('OC-187 AC2: the done arc splits into filled and not populated; the center is the whole ratio', () => {
    renderUI(<ScoreRing ratio={0.98} filled={0.73} label="98% done" />)
    expect(screen.getByRole('img', { name: '98% done' })).toHaveTextContent('98%')
    expect(screen.getByTestId('ring-filled')).toHaveAttribute('data-length', '0.7300')
    expect(screen.getByTestId('ring-not-populated')).toHaveAttribute('data-length', '0.2500')
  })

  it('without a split, one arc', () => {
    renderUI(<ScoreRing ratio={0.65} label="65%" />)
    expect(screen.getByTestId('ring-filled')).toHaveAttribute('data-length', '0.6500')
    expect(screen.queryByTestId('ring-not-populated')).toBeNull()
  })

  it('nothing done: no arc', () => {
    renderUI(<ScoreRing ratio={0} filled={0} label="0%" />)
    expect(screen.queryByTestId('ring-filled')).toBeNull()
    expect(screen.queryByTestId('ring-not-populated')).toBeNull()
  })
})
