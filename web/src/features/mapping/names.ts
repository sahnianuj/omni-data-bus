import { snake } from '@/lib/naming'

/** Mirrors naming.CollectionName: Order Details → order_details. */
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

/** Mirrors naming.FolderName: a collection name without "%", at most 64 characters (OC-038). */
export function folderName(name: string): string {
  return sanitizeCollectionName(name).replace(/%/g, '_').slice(0, 64).replace(/^_+|_+$/g, '') || 'collection'
}
