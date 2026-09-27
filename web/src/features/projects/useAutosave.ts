import { useCallback, useEffect, useRef, useState } from 'react'

export type AutosaveState = 'idle' | 'saving' | 'saved' | 'error'

/**
 * Debounced autosave (OC-007 AC6). `schedule(value)` saves 800 ms after the last change; edits
 * made while a save is in flight are coalesced into one follow-up save; errors expose `retry`.
 */
export function useAutosave<T>(save: (value: T) => Promise<unknown>, delayMs = 800) {
  const [state, setState] = useState<AutosaveState>('idle')
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const inFlight = useRef(false)
  const pending = useRef<{ value: T } | null>(null)
  const last = useRef<{ value: T } | null>(null)
  const saveRef = useRef(save)
  useEffect(() => {
    saveRef.current = save
  }, [save])

  const flush = useCallback(async () => {
    if (inFlight.current) return
    inFlight.current = true
    // Keep saving while edits arrived during the previous save (they coalesce into one).
    while (pending.current) {
      const { value } = pending.current
      pending.current = null
      last.current = { value }
      setState('saving')
      try {
        await saveRef.current(value)
      } catch {
        inFlight.current = false
        setState('error')
        return
      }
    }
    inFlight.current = false
    setState('saved')
  }, [])

  const schedule = useCallback(
    (value: T) => {
      pending.current = { value }
      if (timer.current) clearTimeout(timer.current)
      timer.current = setTimeout(() => void flush(), delayMs)
    },
    [delayMs, flush],
  )

  const retry = useCallback(() => {
    if (last.current && !pending.current) pending.current = last.current
    void flush()
  }, [flush])

  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current)
  }, [])

  return { state, schedule, retry }
}
