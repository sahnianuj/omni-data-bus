/** A bounded undo/redo stack (OC-015 AC6: 100 steps). */
export type History<T> = { past: T[]; present: T; future: T[] }

export const HISTORY_LIMIT = 100

export function initHistory<T>(present: T): History<T> {
  return { past: [], present, future: [] }
}

export function push<T>(h: History<T>, next: T): History<T> {
  if (Object.is(next, h.present)) return h
  const past = [...h.past, h.present]
  if (past.length > HISTORY_LIMIT) past.splice(0, past.length - HISTORY_LIMIT)
  return { past, present: next, future: [] }
}

/** Folds every step pushed since the past had `mark` entries into one undo step. */
export function squash<T>(h: History<T>, mark: number): History<T> {
  if (h.past.length <= mark + 1) return h
  return { past: h.past.slice(0, mark + 1), present: h.present, future: [] }
}

/** Goes back to the state the past had `mark` entries at (dropping what came since). */
export function revert<T>(h: History<T>, mark: number): History<T> {
  if (h.past.length <= mark) return h
  return { past: h.past.slice(0, mark), present: h.past[mark] as T, future: [] }
}

export function undo<T>(h: History<T>): History<T> {
  const prev = h.past[h.past.length - 1]
  if (prev === undefined) return h
  return { past: h.past.slice(0, -1), present: prev, future: [h.present, ...h.future] }
}

export function redo<T>(h: History<T>): History<T> {
  const next = h.future[0]
  if (next === undefined) return h
  return { past: [...h.past, h.present], present: next, future: h.future.slice(1) }
}
