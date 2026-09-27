/** localStorage access that never throws (private mode, blocked storage, tests). */
export const storage = {
  get(key: string): string | null {
    try {
      return window.localStorage.getItem(key)
    } catch {
      return null
    }
  },
  set(key: string, value: string) {
    try {
      window.localStorage.setItem(key, value)
    } catch {
      /* ignore */
    }
  },
}
