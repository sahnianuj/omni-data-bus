import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useAutosave } from './useAutosave'

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

describe('useAutosave', () => {
  it('AC6: debounces and coalesces', async () => {
    let release: () => void = () => {}
    const save = vi.fn(() => new Promise<void>((r) => (release = r)))
    const { result } = renderHook(() => useAutosave<string>(save))

    act(() => {
      result.current.schedule('a')
      vi.advanceTimersByTime(500)
      result.current.schedule('ab')
    })
    expect(save).not.toHaveBeenCalled()
    await act(async () => vi.advanceTimersByTime(800))
    expect(save).toHaveBeenCalledTimes(1)
    expect(save).toHaveBeenLastCalledWith('ab')
    expect(result.current.state).toBe('saving')

    // Two more edits while the first save is in flight → exactly one follow-up save with the latest value.
    act(() => {
      result.current.schedule('abc')
      result.current.schedule('abcd')
    })
    await act(async () => vi.advanceTimersByTime(800))
    expect(save).toHaveBeenCalledTimes(1)
    await act(async () => release())
    expect(save).toHaveBeenCalledTimes(2)
    expect(save).toHaveBeenLastCalledWith('abcd')
    await act(async () => release())
    expect(result.current.state).toBe('saved')
  })

  it('AC6: error state and retry', async () => {
    const save = vi.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce(undefined)
    const { result } = renderHook(() => useAutosave<string>(save))
    act(() => result.current.schedule('x'))
    await act(async () => vi.advanceTimersByTime(800))
    expect(result.current.state).toBe('error')
    await act(async () => result.current.retry())
    expect(save).toHaveBeenCalledTimes(2)
    expect(save).toHaveBeenLastCalledWith('x')
    expect(result.current.state).toBe('saved')
  })
})
