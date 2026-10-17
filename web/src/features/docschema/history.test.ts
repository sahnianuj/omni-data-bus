import { describe, expect, it } from 'vitest'
import { HISTORY_LIMIT, initHistory, push, redo, revert, squash, undo } from './history'

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

  it('OC-142 AC2: squash folds steps into one undo; revert drops them', () => {
    const h = push(push(push(push(initHistory(0), 1), 2), 3), 4) // mark = 1 (past [0]) before 2, 3, 4
    const one = squash(h, 1)
    expect(one.present).toBe(4)
    expect(undo(one).present, 'one undo back to before the steps').toBe(1)
    expect(squash(push(initHistory(0), 1), 0), 'nothing to fold').toEqual(push(initHistory(0), 1))
    const back = revert(h, 1)
    expect(back).toEqual({ past: [0], present: 1, future: [] })
    expect(revert(back, 1)).toBe(back)
  })
})
