import { describe, expect, it } from 'vitest'
import { sniffJson } from './sniffJson'

describe('sniffJson', () => {
  it('AC7: detects JSON Schema vs sample', () => {
    expect(sniffJson('{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}').kind).toBe('json-schema')
    expect(sniffJson('{"type":"object","properties":{"a":{"type":"string"}}}').kind).toBe('json-schema')
    expect(sniffJson('{"type":"customer","name":"Ada"}').kind, 'a document with a type field is a sample').toBe('json-sample')
    expect(sniffJson('[{"a":1},{"a":2}]').kind).toBe('json-sample')
    expect(sniffJson('{"a":1}\n{"a":2}\n').kind).toBe('json-sample')
    expect(sniffJson('{oops').error).toMatch(/Not valid JSON/)
    expect(sniffJson('  ').error).toBe('The file is empty.')
  })
})
