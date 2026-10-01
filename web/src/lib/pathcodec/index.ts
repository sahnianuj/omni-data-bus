import { sha1Hex } from '@/lib/sha1'

// Mirrors backend/internal/pathcodec (OC-039): a document key ↔ a file name valid on Linux, macOS
// and Windows. Both run backend/internal/pathcodec/testdata/pathcodec.json.

export const MAX_NAME = 247
const HASHED_HEAD = 200
const HASH_LEN = 16
const HEX = '0123456789ABCDEF'
const UNSAFE = new Set([...'/\\:*?"<>|%~'].map((c) => c.charCodeAt(0)))
const RESERVED = new Set(['CON', 'PRN', 'AUX', 'NUL', ...[1, 2, 3, 4, 5, 6, 7, 8, 9].flatMap((d) => [`COM${d}`, `LPT${d}`])])

const enc = new TextEncoder()
const dec = new TextDecoder('utf-8', { fatal: true })

const validUtf8 = (b: Uint8Array) => {
  try {
    dec.decode(b)
    return true
  } catch {
    return false
  }
}

/** A key's file name (without extension), and whether it had to be hashed (too long to keep). */
export function encodeKey(key: string): { name: string; hashed: boolean } {
  const k = key.normalize('NFC')
  const bytes = enc.encode(k)
  const dot = k.indexOf('.')
  const reserved = RESERVED.has((dot >= 0 ? k.slice(0, dot) : k).toUpperCase())
  const out: number[] = []
  bytes.forEach((b, i) => {
    const escape = UNSAFE.has(b) || b < 0x20 || b === 0x7f || (i === 0 && (b === 0x2e || b === 0x2d || reserved)) || (i === bytes.length - 1 && (b === 0x2e || b === 0x20))
    if (escape) out.push(0x25, HEX.charCodeAt(b >> 4), HEX.charCodeAt(b & 0xf))
    else out.push(b)
  })
  if (out.length <= MAX_NAME) return { name: dec.decode(new Uint8Array(out)), hashed: false }
  let head = out.slice(0, HASHED_HEAD)
  const pct = head.lastIndexOf(0x25)
  if (pct >= HASHED_HEAD - 2) head = head.slice(0, pct)
  while (head.length > 0 && !validUtf8(new Uint8Array(head))) head = head.slice(0, -1)
  return { name: `${dec.decode(new Uint8Array(head))}~${sha1Hex(k).slice(0, HASH_LEN)}`, hashed: true }
}

/** The key of a file name, or null for a hashed name or one that isn't an encoding. */
export function decodeName(name: string): string | null {
  if (name.includes('~')) return null
  const bytes = enc.encode(name)
  const out: number[] = []
  for (let i = 0; i < bytes.length; i++) {
    if (bytes[i] !== 0x25) {
      out.push(bytes[i]!)
      continue
    }
    const hi = HEX.indexOf(String.fromCharCode(bytes[i + 1] ?? 0))
    const lo = HEX.indexOf(String.fromCharCode(bytes[i + 2] ?? 0))
    if (i + 2 >= bytes.length || hi < 0 || lo < 0) return null
    out.push((hi << 4) | lo)
    i += 2
  }
  try {
    return dec.decode(new Uint8Array(out))
  } catch {
    return null
  }
}

export type Layout = 'flat' | 'sharded' | 'key-path'

/** Where a document lands, relative to the target folder (mirrors the Go directory layout). */
export function documentPath(layout: Layout, folder: string, key: string, gzip = false): string {
  const ext = gzip ? '.json.gz' : '.json'
  const f = folder.replace(/^\/+|\/+$/g, '')
  if (layout === 'key-path') {
    const segs = key.split('::').map((s) => (s === '' ? '%00' : encodeKey(s).name))
    return [f, ...segs.slice(0, -1), segs.at(-1)! + ext].join('/')
  }
  const name = encodeKey(key).name + ext
  if (layout === 'sharded') {
    const h = sha1Hex(key.normalize('NFC'))
    return [f, h.slice(0, 2), h.slice(2, 4), name].join('/')
  }
  return `${f}/${name}`
}
