import { useEffect } from 'react'
import { createStore, useStore } from 'zustand'
import { storage } from './storage'

export const MAXIMIZE_KEY = 'oc-workbench-maximized'

interface MaximizeState {
  /** The user's choice, remembered like the sidebar's. */
  maximized: boolean
  /** A page that can be maximized (the mapping workbench) is open. */
  active: boolean
  toggle: () => void
  setActive: (active: boolean) => void
}

/**
 * Give the workbench the full height: the app's top bar, the project header and the build footer
 * step aside while it is open and maximized. Other pages always keep their chrome.
 */
export const maximizeStore = createStore<MaximizeState>()((set, get) => ({
  maximized: storage.get(MAXIMIZE_KEY) === 'true',
  active: false,
  toggle: () => {
    const next = !get().maximized
    storage.set(MAXIMIZE_KEY, String(next))
    set({ maximized: next })
  },
  setActive: (active) => set({ active }),
}))

/** True when the page chrome should be hidden. */
export const useMaximized = () => useStore(maximizeStore, (s) => s.active && s.maximized)

/** Marks the calling page as maximizable while it is mounted. */
export function useMaximizable() {
  useEffect(() => {
    maximizeStore.getState().setActive(true)
    return () => maximizeStore.getState().setActive(false)
  }, [])
}
