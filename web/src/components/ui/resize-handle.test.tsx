import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { ResizeHandle, usePaneWidth } from './resize-handle'

function Pane({ edge = 'right' as 'left' | 'right' }) {
  const pane = usePaneWidth('test', 300, 200, 400)
  return (
    <div data-testid="pane" style={{ width: pane.width }}>
      <ResizeHandle pane={pane} edge={edge} label="Resize" />
    </div>
  )
}

describe('ResizeHandle', () => {
  beforeEach(() => window.localStorage.clear())

  it('resizes with the keyboard within the limits and remembers the width', () => {
    const { unmount } = render(<Pane />)
    const handle = screen.getByRole('separator', { name: 'Resize' })
    fireEvent.keyDown(handle, { key: 'ArrowRight' })
    expect(handle).toHaveAttribute('aria-valuenow', '316')
    fireEvent.keyDown(handle, { key: 'End' })
    expect(handle).toHaveAttribute('aria-valuenow', '400')
    fireEvent.keyDown(handle, { key: 'ArrowRight' })
    expect(handle).toHaveAttribute('aria-valuenow', '400')
    unmount()
    render(<Pane />)
    expect(screen.getByTestId('pane')).toHaveStyle({ width: '400px' })
  })

  it('widens a right-docked pane when dragged left, and resets on double-click', () => {
    render(<Pane edge="left" />)
    const handle = screen.getByRole('separator', { name: 'Resize' })
    handle.setPointerCapture = () => {}
    handle.hasPointerCapture = () => false
    fireEvent.pointerDown(handle, { button: 0, clientX: 500, pointerId: 1 })
    fireEvent.pointerMove(handle, { clientX: 450, pointerId: 1 })
    fireEvent.pointerUp(handle, { pointerId: 1 })
    expect(handle).toHaveAttribute('aria-valuenow', '350')
    fireEvent.doubleClick(handle)
    expect(handle).toHaveAttribute('aria-valuenow', '300')
  })
})
