/**
 * JSONC to JSON: drops // and /* *\/ comments and trailing commas, leaving strings alone
 * (a "see payments/*.json" description stays as written). Line breaks are kept, so NDJSON
 * and parse-error positions still line up.
 */
export function stripJsonc(text: string): string {
  let out = ''
  let i = 0
  while (i < text.length) {
    const c = text[i]!
    if (c === '"') {
      const start = i++
      while (i < text.length && text[i] !== '"') i += text[i] === '\\' ? 2 : 1
      out += text.slice(start, ++i)
    } else if (c === '/' && text[i + 1] === '/') {
      while (i < text.length && text[i] !== '\n') i++
    } else if (c === '/' && text[i + 1] === '*') {
      const end = text.indexOf('*/', i + 2)
      const stop = end < 0 ? text.length : end + 2
      out += text.slice(i, stop).replace(/[^\n]/g, '')
      i = stop
    } else {
      // A trailing comma: one followed only by whitespace (or comments) before } or ].
      if (c === '}' || c === ']') out = out.replace(/,(\s*)$/, '$1')
      out += c
      i++
    }
  }
  return out
}
