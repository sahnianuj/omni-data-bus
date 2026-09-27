import { afterEach, beforeEach, vi } from 'vitest'

/**
 * jsdom has no layout: every element measures 0×0, so virtualized lists render nothing. This
 * gives every element a fixed size for the tests in the calling file.
 */
export function withFixedLayout(width = 280, height = 600) {
  beforeEach(() => {
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(height)
    vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockReturnValue(width)
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue(DOMRect.fromRect({ width, height }))
  })
  afterEach(() => vi.restoreAllMocks())
}
