import { describe, expect, it } from 'vitest'
import { HISTORY_LIMIT, initHistory, push, redo, undo } from './history'

describe('history', () => {
  it('AC6: undo/redo 100-step cap', () => {
    let h = initHistory(0)
    for (let i = 1; i <= 150; i++) h = push(h, i)
    expect(h.past).toHaveLength(HISTORY_LIMIT)
    for (let i = 0; i < 200; i++) h = undo(h)
    expect(h.present, 'only the last 100 steps are kept').toBe(50)
    h = redo(redo(h))
    expect(h.present).toBe(52)
    h = push(h, 999)
    expect(h.future, 'a new edit clears redo').toEqual([])
    expect(redo(h)).toBe(h)
    expect(push(h, 999)).toBe(h)
  })
})
