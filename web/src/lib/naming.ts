// Mirrors backend/internal/naming: split identifiers into words and re-join them.

const isDigit = (c: string) => c >= '0' && c <= '9'
const isUpper = (c: string) => c !== c.toLowerCase() && c === c.toUpperCase()
const isLower = (c: string) => c !== c.toUpperCase() && c === c.toLowerCase()
const isLetter = (c: string) => c.toLowerCase() !== c.toUpperCase()

/** customerID → [customer, ID]; addr2 → [addr, 2]; URLValue → [URL, Value]. */
export function words(name: string): string[] {
  const out: string[] = []
  let cur = ''
  const chars = [...name]
  chars.forEach((c, i) => {
    if ('_- ./'.includes(c)) {
      if (cur) out.push(cur)
      cur = ''
      return
    }
    if (cur) {
      const prev = cur[cur.length - 1]!
      const next = chars[i + 1] ?? ''
      const split =
        (isDigit(c) !== isDigit(prev) && (isLetter(prev) || isLetter(c))) ||
        (isUpper(c) && isLower(prev)) ||
        (isUpper(c) && isUpper(prev) && next !== '' && isLower(next))
      if (split) {
        out.push(cur)
        cur = ''
      }
    }
    cur += c
  })
  if (cur) out.push(cur)
  return out
}

export function camel(name: string): string {
  const w = words(name).map((s) => s.toLowerCase())
  if (w.length === 0) return name
  return w[0] + w.slice(1).map((s) => s[0]!.toUpperCase() + s.slice(1)).join('')
}

export function snake(name: string): string {
  const w = words(name).map((s) => s.toLowerCase())
  return w.length === 0 ? name : w.join('_')
}

export type NamingStyle = 'camelCase' | 'snake_case' | 'preserve'

export function rename(name: string, style: NamingStyle = 'camelCase'): string {
  if (style === 'preserve') return name
  return style === 'snake_case' ? snake(name) : camel(name)
}
