import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { ResizeHandle, usePaneSize } from './resize-handle'

function Pane({ edge = 'right' as 'left' | 'right' | 'top' | 'bottom' }) {
  const pane = usePaneSize('test', 300, 200, 400)
  const vertical = edge === 'top' || edge === 'bottom'
  return (
    <div data-testid="pane" style={vertical ? { height: pane.size } : { width: pane.size }}>
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

  it('a top handle resizes the height: dragging or ↑ grows the pane, ↓ shrinks it', () => {
    render(<Pane edge="top" />)
    const handle = screen.getByRole('separator', { name: 'Resize' })
    expect(handle).toHaveAttribute('aria-orientation', 'horizontal')
    fireEvent.keyDown(handle, { key: 'ArrowUp' })
    expect(handle).toHaveAttribute('aria-valuenow', '316')
    fireEvent.keyDown(handle, { key: 'ArrowDown' })
    fireEvent.keyDown(handle, { key: 'ArrowDown' })
    expect(handle).toHaveAttribute('aria-valuenow', '284')
    fireEvent.keyDown(handle, { key: 'ArrowRight' })
    expect(handle).toHaveAttribute('aria-valuenow', '284')
    handle.setPointerCapture = () => {}
    handle.hasPointerCapture = () => false
    fireEvent.pointerDown(handle, { button: 0, clientX: 0, clientY: 500, pointerId: 1 })
    fireEvent.pointerMove(handle, { clientX: 0, clientY: 440, pointerId: 1 })
    fireEvent.pointerUp(handle, { pointerId: 1 })
    expect(handle).toHaveAttribute('aria-valuenow', '344')
    expect(screen.getByTestId('pane')).toHaveStyle({ height: '344px' })
  })
})
