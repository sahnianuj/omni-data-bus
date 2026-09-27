/** WCAG 2.1 relative luminance and contrast ratio helpers (used by token tests). */
function channel(c: number) {
  const s = c / 255
  return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
}

export function hexToRgb(hex: string): [number, number, number] {
  const h = hex.replace('#', '')
  const full = h.length === 3 ? [...h].map((x) => x + x).join('') : h
  const n = parseInt(full, 16)
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255]
}

export function luminance(hex: string) {
  const [r, g, b] = hexToRgb(hex)
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b)
}

export function contrast(a: string, b: string) {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number]
  return (hi + 0.05) / (lo + 0.05)
}

/** CIE76 ΔE between two colors in Lab space. */
export function deltaE(a: string, b: string) {
  const lab = (hex: string) => {
    const [r, g, bl] = hexToRgb(hex).map(channel) as [number, number, number]
    const x = (r * 0.4124 + g * 0.3576 + bl * 0.1805) / 0.95047
    const y = r * 0.2126 + g * 0.7152 + bl * 0.0722
    const z = (r * 0.0193 + g * 0.1192 + bl * 0.9505) / 1.08883
    const f = (t: number) => (t > 0.008856 ? Math.cbrt(t) : 7.787 * t + 16 / 116)
    return [116 * f(y) - 16, 500 * (f(x) - f(y)), 200 * (f(y) - f(z))]
  }
  const [l1, a1, b1] = lab(a) as [number, number, number]
  const [l2, a2, b2] = lab(b) as [number, number, number]
  return Math.hypot(l1 - l2, a1 - a2, b1 - b2)
}
