import { describe, expect, it } from 'vitest'
import { columns } from '@/test/mapping'
import { analyzeKey, insertToken, tokenAtCaret } from './analyzeKey'

describe('analyzeKey', () => {
  it('AC3: PK coverage is unique, a non-key column may collide, $uuid is not idempotent', () => {
    const pk = columns.find((c) => c.isPrimaryKey)!.name
    const plain = columns.find((c) => !c.isPrimaryKey && !c.isUnique)!.name
    expect(analyzeKey(`customer::{${pk}}`, columns)).toMatchObject({ ok: true, verdict: 'unique' })
    expect(analyzeKey(`customer::{${plain}}`, columns)).toMatchObject({ ok: true, verdict: 'may-collide' })
    expect(analyzeKey('{$uuid}', columns)).toMatchObject({ ok: true, verdict: 'not-idempotent' })
  })

  it('reports unknown columns and parse errors with a position', () => {
    expect(analyzeKey('x::{nope}', columns)).toMatchObject({ ok: true, unknown: ['nope'] })
    expect(analyzeKey('x::{id', columns)).toMatchObject({ ok: false, pos: 3 })
  })
})

describe('token autocomplete', () => {
  it('finds the open token at the caret', () => {
    expect(tokenAtCaret('customer::{na', 13)).toEqual({ start: 10, query: 'na' })
    expect(tokenAtCaret('customer::{id}', 14)).toBeNull()
    expect(tokenAtCaret('a{{', 3)).toBeNull()
    expect(tokenAtCaret('{id|lo', 6)).toBeNull()
  })

  it('AC6: inserts {col} over the partial token', () => {
    expect(insertToken('customer::{na', 13, 10, 'name')).toEqual({ value: 'customer::{name}', caret: 16 })
    expect(insertToken('a::{}::b', 4, 3, 'id')).toEqual({ value: 'a::{id}::b', caret: 7 })
  })
})
