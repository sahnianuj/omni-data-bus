/**
 * The passphrase rule of sealed exports (OC-052), as the server applies it
 * (internal/export/seal.go PassphraseProblem): at least 12 characters and about 50 bits of
 * estimated strength. The server also refuses common passwords and is the final check.
 */
export const MIN_PASSPHRASE = 12
const MIN_BITS = 50
const WORD_BITS = Math.log2(7776) // one diceware word

/** Estimated strength in bits: dictionary-like words of a phrase count as diceware words. */
export function passphraseBits(p: string): number {
  const parts = p.toLowerCase().split(/[ \-._]+/).filter(Boolean)
  const words = parts.filter((w) => /^[a-z]{3,}$/.test(w))
  if (parts.length >= 4 && words.length === parts.length) return words.length * WORD_BITS
  return runBits(p)
}

function runBits(s: string): number {
  let pool = 0
  if (/[a-z]/.test(s)) pool += 26
  if (/[A-Z]/.test(s)) pool += 26
  if (/[0-9]/.test(s)) pool += 10
  if (/[^a-zA-Z0-9]/.test(s)) pool += 33
  if (pool === 0) return 0
  const distinct = new Set<string>()
  let steps = 0
  let prev = -10
  for (const ch of s) {
    const c = ch.codePointAt(0) ?? 0
    // "abc" and "321" add nothing past their first character.
    if (c !== prev + 1 && c !== prev - 1) {
      distinct.add(ch)
      steps++
    }
    prev = c
  }
  return Math.min(distinct.size, steps) * Math.log2(pool)
}

/** Returns why a passphrase is too weak ("short" | "weak"), or null. */
export function passphraseProblem(p: string): 'short' | 'weak' | null {
  if ([...p].length < MIN_PASSPHRASE) return 'short'
  if (passphraseBits(p) < MIN_BITS) return 'weak'
  return null
}
