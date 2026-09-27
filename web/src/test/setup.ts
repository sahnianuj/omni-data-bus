import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'

afterEach(() => cleanup())
import "@/i18n"
import { installMatchMedia } from './matchMedia'
import { beforeEach } from 'vitest'

beforeEach(() => {
  installMatchMedia(false)
  window.localStorage.clear()
  document.documentElement.classList.remove('dark')
})

// jsdom lacks these; Radix and cmdk use them.
class RO {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= RO as unknown as typeof ResizeObserver
Element.prototype.scrollIntoView ??= () => {}
Element.prototype.setPointerCapture ??= () => {}
Element.prototype.releasePointerCapture ??= () => {}
Element.prototype.hasPointerCapture ??= () => false
