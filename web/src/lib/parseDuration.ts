const units: Record<string, number> = { s: 1, m: 60, h: 3600, d: 86400 }

/** "30d", "12h", "90m", "45s" or plain seconds ("3600") → seconds; null when invalid. */
export function parseDuration(input: string): number | null {
  const m = /^\s*(\d+)\s*([smhd]?)\s*$/i.exec(input)
  if (!m) return null
  const n = Number(m[1]) * units[(m[2] || 's').toLowerCase()]!
  return Number.isSafeInteger(n) ? n : null
}
