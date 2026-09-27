import { describe, expect, it } from 'vitest'
import { parsePgUri } from './parsePgUri'

describe('parsePgUri', () => {
  it('AC2: parses all parts incl. encoded password', () => {
    expect(parsePgUri('postgresql://app:s3cr%40t@db.example.com:6543/shop?sslmode=verify-full')).toEqual({
      host: 'db.example.com', port: 6543, database: 'shop', username: 'app', password: 's3cr@t', sslMode: 'verify-full',
    })
  })

  it('AC2: accepts postgres:// and partial URIs', () => {
    expect(parsePgUri('postgres://db.internal/inventory')).toEqual({ host: 'db.internal', database: 'inventory' })
  })

  it('AC2: rejects non-postgres scheme', () => {
    expect(parsePgUri('mysql://u:p@h/db')).toBeNull()
    expect(parsePgUri('db.example.com')).toBeNull()
    expect(parsePgUri('postgresql://')).toBeNull()
  })

  it('AC2: supports IPv6 host [::1]', () => {
    expect(parsePgUri('postgresql://u@[::1]:5432/db')).toMatchObject({ host: '::1', port: 5432 })
  })

  it('ignores unknown sslmode values', () => {
    expect(parsePgUri('postgresql://h/db?sslmode=sometimes')?.sslMode).toBeUndefined()
  })
})
