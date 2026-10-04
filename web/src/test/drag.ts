import { createEvent, fireEvent } from '@testing-library/react'

/** A DataTransfer stand-in whose types follow what drag start put into it. */
export function dataTransfer() {
  const data = new Map<string, string>()
  return {
    setData: (k: string, v: string) => data.set(k, v),
    getData: (k: string) => data.get(k) ?? '',
    get types() {
      return [...data.keys()]
    },
    dropEffect: 'none',
    effectAllowed: 'all',
  }
}

/**
 * Drags one element onto another. jsdom has no DragEvent, so clientY (the pointer's height,
 * which picks before / inside / after) is set on the events by hand; without it, rows count
 * the drop as their middle.
 */
export function dragOnto(from: HTMLElement, onto: HTMLElement, clientY?: number) {
  const dt = dataTransfer()
  fireEvent.dragStart(from, { dataTransfer: dt })
  for (const make of [createEvent.dragOver, createEvent.drop]) {
    const ev = make(onto, { dataTransfer: dt })
    if (clientY !== undefined) Object.defineProperty(ev, 'clientY', { value: clientY })
    fireEvent(onto, ev)
  }
}
