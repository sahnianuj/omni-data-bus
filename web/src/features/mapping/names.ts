import { snake } from '@/lib/naming'

/** Mirrors mappings.SanitizeCollectionName: Order Details → order_details. */
export function sanitizeCollectionName(table: string): string {
  let out = ''
  for (const ch of snake(table)) {
    if (/[a-z0-9_%-]/.test(ch)) out += ch
    else if (/[A-Z]/.test(ch)) out += ch.toLowerCase()
    else out += '_'
  }
  out = out.replace(/^[_%]+/, '').replace(/_+/g, '_').replace(/^_+|_+$/g, '')
  if (out.length > 251) out = out.slice(0, 251).replace(/_+$/, '')
  return out || 'collection'
}
