export type ParsedPgUri = {
  host: string
  port?: number
  database?: string
  username?: string
  password?: string
  sslMode?: 'disable' | 'prefer' | 'require' | 'verify-ca' | 'verify-full'
}

const SSL_MODES = new Set(['disable', 'prefer', 'require', 'verify-ca', 'verify-full'])

/**
 * Parses a postgres:// URI into form fields (OC-009 AC2). Runs only in the browser; the raw URI
 * is never sent to the server. Returns null for anything that isn't a PostgreSQL URI.
 */
export function parsePgUri(input: string): ParsedPgUri | null {
  const raw = input.trim()
  const m = raw.match(/^(postgres(?:ql)?):\/\//i)
  if (!m) return null
  let url: URL
  try {
    // URL can't parse custom schemes' hosts reliably; swap in http to reuse its parser.
    url = new URL('http://' + raw.slice(m[0].length))
  } catch {
    return null
  }
  if (!url.hostname) return null
  const out: ParsedPgUri = { host: url.hostname.replace(/^\[|\]$/g, '') }
  if (url.port) out.port = Number(url.port)
  const db = decodeURIComponent(url.pathname.replace(/^\//, ''))
  if (db) out.database = db
  if (url.username) out.username = decodeURIComponent(url.username)
  if (url.password) out.password = decodeURIComponent(url.password)
  const ssl = url.searchParams.get('sslmode')
  if (ssl && SSL_MODES.has(ssl)) out.sslMode = ssl as ParsedPgUri['sslMode']
  return out
}
