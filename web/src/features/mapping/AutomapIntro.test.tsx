import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { stats } from '@/test/mapping'
import { renderUI } from '@/test/render'
import { AutomapIntro } from './AutomapIntro'

beforeEach(() => window.localStorage.clear())

describe('AutomapIntro', () => {
  it('AC5: shows once per mapping', async () => {
    const onReview = vi.fn()
    const { unmount } = renderUI(<AutomapIntro mappingId="m1" stats={stats()} onReview={onReview} />)
    expect(screen.getByRole('status')).toHaveTextContent('Auto-mapped 10 of 23 columns. 4 suggestions are waiting for your review.')
    await userEvent.click(screen.getByRole('button', { name: 'Review suggestions' }))
    expect(onReview).toHaveBeenCalledOnce()
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
    unmount()

    const again = renderUI(<AutomapIntro mappingId="m1" stats={stats()} onReview={onReview} />)
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
    again.unmount()

    renderUI(<AutomapIntro mappingId="m2" stats={stats()} onReview={onReview} />)
    await userEvent.click(screen.getByRole('button', { name: 'Dismiss' }))
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })

  it('stays hidden when nothing was auto-mapped', () => {
    renderUI(<AutomapIntro mappingId="m3" stats={stats({ autoMapped: 0, suggested: 0 })} onReview={() => {}} />)
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })
})
