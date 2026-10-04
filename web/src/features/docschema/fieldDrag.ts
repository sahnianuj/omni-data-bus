import type { DragEvent } from 'react'
import type { DropPos } from './docschemaOps'

/** A target field being dragged to a new place, in the Canvas, Composer or Design pane. */
export const DND_FIELD = 'application/x-odb-field'

export const isFieldDrag = (e: DragEvent) => e.dataTransfer.types.includes(DND_FIELD)

export function startFieldDrag(e: DragEvent, id: string) {
  e.stopPropagation()
  e.dataTransfer.setData(DND_FIELD, id)
  e.dataTransfer.effectAllowed = 'move'
}

/** Where a field dropped on a row lands: a container's middle is inside it, otherwise the nearer edge. */
export function dropPosition(e: DragEvent, container: boolean): DropPos {
  const r = e.currentTarget.getBoundingClientRect()
  const y = r.height && Number.isFinite(e.clientY) ? (e.clientY - r.top) / r.height : 0.5
  if (container) return y < 0.25 ? 'before' : y > 0.75 ? 'after' : 'inside'
  return y < 0.5 ? 'before' : 'after'
}

/** The drop indicator: a line above or below the row, or a ring round a container. */
export const dropIndicator: Record<DropPos, string> = {
  before: 'shadow-[inset_0_2px_0_var(--color-primary)]',
  after: 'shadow-[inset_0_-2px_0_var(--color-primary)]',
  inside: 'ring-2 ring-inset ring-primary',
}
