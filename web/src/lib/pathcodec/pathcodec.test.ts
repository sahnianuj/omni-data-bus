import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { decodeName, documentPath, encodeKey } from '.'

const fixture = JSON.parse(readFileSync(resolve(process.cwd(), '../backend/internal/pathcodec/testdata/pathcodec.json'), 'utf8')) as {
  cases: { key: string; name: string; hashed: boolean }[]
}

describe('pathcodec', () => {
  it('AC2: matches server fixtures', () => {
    expect(fixture.cases.length).toBeGreaterThan(20)
    for (const c of fixture.cases) {
      expect(encodeKey(c.key), JSON.stringify(c.key)).toEqual({ name: c.name, hashed: c.hashed })
      expect(decodeName(c.name), JSON.stringify(c.key)).toBe(c.hashed ? null : c.key.normalize('NFC'))
    }
  })

  it('rejects names that are not encodings', () => {
    for (const bad of ['%', '%4', '%zz', 'a~0123456789abcdef']) expect(decodeName(bad)).toBeNull()
  })

  it('AC1: paths per layout (same as the server)', () => {
    expect(documentPath('flat', 'orders', '1001')).toBe('orders/1001.json')
    expect(documentPath('flat', 'orders', '1001', true)).toBe('orders/1001.json.gz')
    expect(documentPath('sharded', 'orders', '1001')).toMatch(/^orders\/[0-9a-f]{2}\/[0-9a-f]{2}\/1001\.json$/)
    expect(documentPath('key-path', 'orders', '2026::10::1001')).toBe('orders/2026/10/1001.json')
    expect(documentPath('key-path', '/orders/', 'a::::b')).toBe('orders/a/%00/b.json')
  })
})
