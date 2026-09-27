import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { parseKeyTemplate, renderKey, TemplateError, templateColumns } from './renderKey'

type Case = { name: string; template: string; table?: string; columns: string[]; values: unknown[]; expected?: string; error?: string; parseErrorPos?: number }
const fixture = JSON.parse(readFileSync(resolve(process.cwd(), '../backend/internal/mapping/testdata/key-templates.json'), 'utf8')) as { cases: Case[] }

describe('renderKey', () => {
  it('AC6: client renderer matches server fixtures', () => {
    for (const c of fixture.cases) {
      if (c.parseErrorPos !== undefined) {
        let err: unknown
        try {
          parseKeyTemplate(c.template)
        } catch (e) {
          err = e
        }
        expect(err, c.name).toBeInstanceOf(TemplateError)
        expect((err as TemplateError).pos, c.name).toBe(c.parseErrorPos)
        continue
      }
      const row = Object.fromEntries(c.columns.map((col, i) => [col, c.values[i]]))
      const res = renderKey(parseKeyTemplate(c.template), row, c.table)
      if (c.error) expect(res, c.name).toMatchObject({ error: c.error })
      else expect(res, c.name).toEqual({ key: c.expected })
    }
  })

  it('columns, limits and specials', () => {
    const segs = parseKeyTemplate('a::{x}::{y}::{x}')
    expect(templateColumns(segs)).toEqual(['x', 'y'])
    expect(renderKey(parseKeyTemplate('{v}'), { v: 'x'.repeat(300) })).toMatchObject({ error: 'KEY_TOO_LONG' })
    expect(renderKey(parseKeyTemplate('{$uuid}'), {})).toEqual({ key: '{uuid}' })
    expect(() => parseKeyTemplate('{$table|lower}')).toThrow(TemplateError)
    expect(() => parseKeyTemplate('')).toThrow('empty')
    expect(renderKey(parseKeyTemplate('{n}'), { n: 2.5 })).toEqual({ key: '2.5' })
  })
})
