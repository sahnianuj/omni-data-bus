import { useCallback, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react'
import { cn } from '@/lib/utils'

const STEP = 16

const clamp = (w: number, min: number, max: number) => Math.min(Math.max(w, min), max)

/**
 * A pane size (its width, or its height for a top/bottom handle) the user can drag, remembered
 * in this browser under key. Browser storage can be unavailable or throw (private windows):
 * every access is guarded.
 */
export function usePaneSize(key: string, initial: number, min: number, max: number) {
  const storageKey = `odb.pane.${key}`
  const [size, setSizeState] = useState(() => {
    try {
      const v = Number(window.localStorage.getItem(storageKey))
      return v ? clamp(v, min, max) : initial
    } catch {
      return initial
    }
  })
  const setSize = useCallback(
    (w: number) => {
      const next = clamp(Math.round(w), min, max)
      setSizeState(next)
      try {
        window.localStorage.setItem(storageKey, String(next))
      } catch {
        // best effort
      }
    },
    [storageKey, min, max],
  )
  return { size, setSize, min, max, initial }
}

type Props = {
  /** The pane being resized, from usePaneSize. */
  pane: ReturnType<typeof usePaneSize>
  /**
   * Which edge of the pane the handle sits on: dragging away from the pane grows it. Left and
   * right resize its width, top and bottom its height.
   */
  edge: 'left' | 'right' | 'top' | 'bottom'
  label: string
  className?: string
}

/**
 * A splitter on a pane's edge: drag it, use the arrow keys (Home/End for the limits) when
 * focused, or double-click to restore the default size. Place it inside a `relative` pane.
 */
export function ResizeHandle({ pane, edge, label, className }: Props) {
  const drag = useRef<{ at: number; size: number } | null>(null)
  const vertical = edge === 'top' || edge === 'bottom'
  const dir = edge === 'right' || edge === 'bottom' ? 1 : -1
  const pos = (e: PointerEvent) => (vertical ? e.clientY : e.clientX)

  const onPointerDown = (e: PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return
    e.preventDefault()
    e.currentTarget.setPointerCapture(e.pointerId)
    drag.current = { at: pos(e), size: pane.size }
  }
  const onPointerMove = (e: PointerEvent<HTMLDivElement>) => {
    if (!drag.current) return
    pane.setSize(drag.current.size + dir * (pos(e) - drag.current.at))
  }
  const onPointerUp = (e: PointerEvent<HTMLDivElement>) => {
    drag.current = null
    if (e.currentTarget.hasPointerCapture(e.pointerId)) e.currentTarget.releasePointerCapture(e.pointerId)
  }
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const by: Record<string, number> = vertical ? { ArrowDown: STEP * dir, ArrowUp: -STEP * dir } : { ArrowRight: STEP * dir, ArrowLeft: -STEP * dir }
    if (e.key in by) pane.setSize(pane.size + by[e.key]!)
    else if (e.key === 'Home') pane.setSize(pane.min)
    else if (e.key === 'End') pane.setSize(pane.max)
    else return
    e.preventDefault()
    e.stopPropagation()
  }

  return (
    <div
      role="separator"
      aria-orientation={vertical ? 'horizontal' : 'vertical'}
      aria-label={label}
      aria-valuenow={pane.size}
      aria-valuemin={pane.min}
      aria-valuemax={pane.max}
      tabIndex={0}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
      onDoubleClick={() => pane.setSize(pane.initial)}
      onKeyDown={onKeyDown}
      className={cn(
        'absolute z-10 touch-none select-none after:absolute after:bg-transparent after:transition-colors',
        'hover:after:bg-primary focus-visible:outline-none focus-visible:after:bg-primary active:after:bg-primary',
        vertical
          ? 'inset-x-0 h-2 cursor-row-resize after:inset-x-0 after:top-1/2 after:h-0.5 after:-translate-y-1/2'
          : 'inset-y-0 w-2 cursor-col-resize after:inset-y-0 after:left-1/2 after:w-0.5 after:-translate-x-1/2',
        { left: '-left-1', right: '-right-1', top: '-top-1', bottom: '-bottom-1' }[edge],
        className,
      )}
    />
  )
}
