import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { describeError, errorCatalog } from './errorCatalog'

describe('errorCatalog', () => {
  it('AC7: every server error code has a message', () => {
    // The RowErrorCode enum in the OpenAPI spec is what the server can store (OC-026).
    const spec = readFileSync(resolve(__dirname, '../../../../api/openapi.yaml'), 'utf8')
    const block = spec.slice(spec.indexOf('    RowErrorCode:'))
    const enumLine = block.split('\n').find((l) => l.trim().startsWith('enum:'))!
    const codes = enumLine.slice(enumLine.indexOf('[') + 1, enumLine.indexOf(']')).split(',').map((c) => c.trim())
    expect(codes.length).toBeGreaterThan(5)
    expect(Object.keys(errorCatalog).sort()).toEqual([...codes].sort())
    for (const c of codes) {
      expect(errorCatalog[c as keyof typeof errorCatalog].title).not.toBe('')
    }
    expect(describeError('SOMETHING_NEW')).toEqual({ title: 'SOMETHING_NEW', hint: '' })
  })
})
