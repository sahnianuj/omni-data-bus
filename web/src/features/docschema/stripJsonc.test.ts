import { describe, expect, it } from 'vitest'
import { sniffJson } from './sniffJson'
import { stripJsonc } from './stripJsonc'

describe('stripJsonc', () => {
  it('drops comments and trailing commas, keeps strings', () => {
    const src = '{\n  // a note\n  "a": "see x/*.json // not a comment", /* gone */\n  "b": [1, 2,],\n  "c": "a,}",\n}'
    expect(JSON.parse(stripJsonc(src))).toEqual({ a: 'see x/*.json // not a comment', b: [1, 2], c: 'a,}' })
  })

  it('keeps escaped quotes and line numbers', () => {
    expect(JSON.parse(stripJsonc('{"q": "say \\"hi\\" // ok"}'))).toEqual({ q: 'say "hi" // ok' })
    expect(stripJsonc('/* a\nb */{}').split('\n')).toHaveLength(2)
  })

  it('sniffs a commented JSON Schema', () => {
    expect(sniffJson('{\n  "$schema": "https://json-schema.org/draft/2020-12/schema",\n  // why\n  "type": "object",\n}').kind).toBe('json-schema')
  })
})
