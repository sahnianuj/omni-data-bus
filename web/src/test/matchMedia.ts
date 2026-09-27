type Listener = (e: MediaQueryListEvent) => void

/** Controllable window.matchMedia for jsdom. */
export function installMatchMedia(initialDark = false) {
  let dark = initialDark
  const listeners = new Set<Listener>()
  window.matchMedia = ((query: string) => ({
    matches: query.includes('dark') ? dark : false,
    media: query,
    onchange: null,
    addEventListener: (_: string, l: Listener) => listeners.add(l),
    removeEventListener: (_: string, l: Listener) => listeners.delete(l),
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => true,
  })) as unknown as typeof window.matchMedia
  return {
    setDark(next: boolean) {
      dark = next
      listeners.forEach((l) => l({ matches: next } as MediaQueryListEvent))
    },
  }
}
