import { useCallback, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react'
import { cn } from '@/lib/utils'

const STEP = 16

const clamp = (w: number, min: number, max: number) => Math.min(Math.max(w, min), max)

/**
 * A pane width the user can drag, remembered in this browser under key. Browser storage can be
 * unavailable or throw (private windows): every access is guarded.
 */
export function usePaneWidth(key: string, initial: number, min: number, max: number) {
  const storageKey = `odb.pane.${key}`
  const [width, setWidthState] = useState(() => {
    try {
      const v = Number(window.localStorage.getItem(storageKey))
      return v ? clamp(v, min, max) : initial
    } catch {
      return initial
    }
  })
  const setWidth = useCallback(
    (w: number) => {
      const next = clamp(Math.round(w), min, max)
      setWidthState(next)
      try {
        window.localStorage.setItem(storageKey, String(next))
      } catch {
        // best effort
      }
    },
    [storageKey, min, max],
  )
  return { width, setWidth, min, max, initial }
}

type Props = {
  /** The pane being resized, from usePaneWidth. */
  pane: ReturnType<typeof usePaneWidth>
  /** Which edge of the pane the handle sits on: dragging away from the pane widens it. */
  edge: 'left' | 'right'
  label: string
  className?: string
}

/**
 * A vertical splitter on a pane's edge: drag it, use ←/→ (Home/End for the limits) when
 * focused, or double-click to restore the default width. Place it inside a `relative` pane.
 */
export function ResizeHandle({ pane, edge, label, className }: Props) {
  const drag = useRef<{ x: number; w: number } | null>(null)
  const dir = edge === 'right' ? 1 : -1

  const onPointerDown = (e: PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return
    e.preventDefault()
    e.currentTarget.setPointerCapture(e.pointerId)
    drag.current = { x: e.clientX, w: pane.width }
  }
  const onPointerMove = (e: PointerEvent<HTMLDivElement>) => {
    if (!drag.current) return
    pane.setWidth(drag.current.w + dir * (e.clientX - drag.current.x))
  }
  const onPointerUp = (e: PointerEvent<HTMLDivElement>) => {
    drag.current = null
    if (e.currentTarget.hasPointerCapture(e.pointerId)) e.currentTarget.releasePointerCapture(e.pointerId)
  }
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const by: Record<string, number> = { ArrowRight: STEP * dir, ArrowLeft: -STEP * dir }
    if (e.key in by) pane.setWidth(pane.width + by[e.key]!)
    else if (e.key === 'Home') pane.setWidth(pane.min)
    else if (e.key === 'End') pane.setWidth(pane.max)
    else return
    e.preventDefault()
    e.stopPropagation()
  }

  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={label}
      aria-valuenow={pane.width}
      aria-valuemin={pane.min}
      aria-valuemax={pane.max}
      tabIndex={0}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
      onDoubleClick={() => pane.setWidth(pane.initial)}
      onKeyDown={onKeyDown}
      className={cn(
        'absolute inset-y-0 z-10 w-2 cursor-col-resize touch-none select-none',
        'after:absolute after:inset-y-0 after:left-1/2 after:w-0.5 after:-translate-x-1/2 after:bg-transparent after:transition-colors',
        'hover:after:bg-primary focus-visible:outline-none focus-visible:after:bg-primary active:after:bg-primary',
        edge === 'right' ? '-right-1' : '-left-1',
        className,
      )}
    />
  )
}
