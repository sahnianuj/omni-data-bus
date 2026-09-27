export type Sniff = { kind: 'json-schema' | 'json-sample'; error?: undefined } | { kind?: undefined; error: string }

/** Classifies dropped JSON: a JSON Schema (has $schema, or type + properties) or sample documents. */
export function sniffJson(text: string): Sniff {
  const trimmed = text.trim()
  if (!trimmed) return { error: 'The file is empty.' }
  let value: unknown
  try {
    value = JSON.parse(trimmed)
  } catch {
    // NDJSON: one object per line.
    const lines = trimmed.split('\n').filter((l) => l.trim())
    try {
      lines.forEach((l) => JSON.parse(l))
      return { kind: 'json-sample' }
    } catch (e) {
      return { error: `Not valid JSON: ${(e as Error).message}` }
    }
  }
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    const o = value as Record<string, unknown>
    if (typeof o.$schema === 'string' || (o.type === 'object' && typeof o.properties === 'object')) return { kind: 'json-schema' }
  }
  return { kind: 'json-sample' }
}
