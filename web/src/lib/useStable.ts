import { useCallback, useLayoutEffect, useRef } from 'react'

/**
 * Returns a callback whose identity never changes but which always calls the latest `fn`, so
 * memoized children don't re-render just because a parent handler closed over new state.
 * Only call the result from event handlers, never during render.
 */
export function useStable<A extends unknown[], R>(fn: (...args: A) => R): (...args: A) => R {
  const ref = useRef(fn)
  useLayoutEffect(() => {
    ref.current = fn
  })
  return useCallback((...args: A) => ref.current(...args), [])
}
