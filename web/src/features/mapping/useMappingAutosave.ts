import { useCallback, useEffect, useRef, useState } from 'react'
import type { StoreApi } from 'zustand'
import { ApiError } from '@/lib/api/client'
import { fetchMapping, saveMapping } from './api'
import type { WorkbenchState } from './store'
import type { MappingView } from './types'

export type SaveState = 'idle' | 'saving' | 'saved' | 'error' | 'conflict'

/**
 * Saves the workbench spec 800 ms after the last edit, with If-Match on the store's version.
 * A 412 flags a conflict and stops saving until the user reloads or overwrites. Leaving the page
 * saves pending edits at once; onSaved also hears of saves that finish after it was left.
 */
export function useMappingAutosave(store: StoreApi<WorkbenchState>, projectId: string, delayMs = 800, onSaved?: (view: MappingView) => void) {
  const [state, setState] = useState<SaveState>('idle')
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const running = useRef<Promise<void> | null>(null)
  const savedRevision = useRef(store.getState().revision)

  const run = useCallback((): Promise<void> => {
    if (running.current) return running.current
    const loop = async () => {
      // Keep going while edits arrived during the previous save (they coalesce).
      while (store.getState().revision !== savedRevision.current && !store.getState().conflict) {
        const s = store.getState()
        const revision = s.revision
        setState('saving')
        try {
          const view = await saveMapping(projectId, s.view.id, s.version, s.history.present)
          savedRevision.current = revision
          store.getState().saved(view)
          onSaved?.(view)
        } catch (e) {
          if (e instanceof ApiError && e.code === 'MAPPING_VERSION_CONFLICT') {
            store.getState().setConflict(true)
            setState('conflict')
          } else setState('error')
          return
        }
      }
      if (!store.getState().conflict) setState((prev) => (prev === 'saving' ? 'saved' : prev))
    }
    running.current = loop().finally(() => {
      running.current = null
    })
    return running.current
  }, [store, projectId, onSaved])

  useEffect(
    () =>
      store.subscribe((s, prev) => {
        if (s.revision === prev.revision || s.conflict) return
        if (timer.current) clearTimeout(timer.current)
        timer.current = setTimeout(() => void run(), delayMs)
      }),
    [store, run, delayMs],
  )
  useEffect(
    () => () => {
      if (!timer.current) return
      clearTimeout(timer.current)
      void run() // an edit still waiting for its save: save it now, as the page goes
    },
    [run],
  )

  /** Saves pending edits immediately (before validate, preview of the stored spec, or leaving). */
  const saveNow = useCallback(async () => {
    if (timer.current) clearTimeout(timer.current)
    await run()
  }, [run])

  /** Conflict: drop local edits and take the server's copy. */
  const reload = useCallback(async () => {
    const view = await fetchMapping(projectId, store.getState().view.id)
    store.getState().replace(view)
    savedRevision.current = store.getState().revision
    setState('saved')
  }, [store, projectId])

  /** Conflict: write the local spec over the server's newer version. */
  const overwrite = useCallback(async () => {
    const s = store.getState()
    const latest = await fetchMapping(projectId, s.view.id)
    const view = await saveMapping(projectId, s.view.id, latest.version, s.history.present)
    savedRevision.current = s.revision
    store.getState().saved(view)
    setState('saved')
  }, [store, projectId])

  const retry = useCallback(() => void run(), [run])

  return { state, saveNow, reload, overwrite, retry, dirty: () => store.getState().revision !== savedRevision.current }
}
