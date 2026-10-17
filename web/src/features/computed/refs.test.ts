import { describe, expect, it } from 'vitest'
import { move, rename } from '@/features/docschema/docschemaOps'
import { deleteNode } from '@/features/composer/composerOps'
import { setComputed } from '@/features/mapping/mappingOps'
import type { MappingSpec } from '@/features/mapping/types'
import { followDesign, usesAndUsedBy } from './refs'

const base = (): MappingSpec =>
  ({
    specVersion: 2, mode: 'composed', key: { template: '' }, typeField: { enabled: false, name: '', value: '' }, nullHandling: 'include', fieldMappings: [], relations: [],
    docSchema: {
      source: 'generated',
      root: {
        id: 'fld_root', name: '$', type: 'object',
        children: [
          { id: 'f_first', name: 'firstName', type: 'string' },
          { id: 'f_last', name: 'lastName', type: 'string' },
          { id: 'f_full', name: 'fullName', type: 'string' },
          { id: 'f_items', name: 'items', type: 'array', items: { id: 'f_item', name: '[]', type: 'object', children: [{ id: 'f_sku', name: 'sku', type: 'string' }, { id: 'f_label', name: 'label', type: 'string' }] } },
          { id: 'f_tag', name: 'tag', type: 'string' },
        ],
      },
    },
  }) as unknown as MappingSpec

const exprOf = (s: MappingSpec, id: string) => s.fieldMappings.find((m) => m.targetFieldId === id)!.computed!.expr

describe('computed fields follow the document', () => {
  it('OC-139 AC1: a rename rewrites every reference', () => {
    let s = setComputed(base(), 'f_full', { lang: 'template', expr: '${doc.firstName} ${ doc.lastName } (${self.firstName})' })
    s = setComputed(s, 'f_tag', { lang: 'template', expr: '#${doc.fullName}' })
    const next = followDesign(s, { ...s, docSchema: rename(s.docSchema, 'f_first', 'givenName') })
    expect(exprOf(next, 'f_full')).toBe('${doc.givenName} ${ doc.lastName } (${self.givenName})')
    expect(exprOf(next, 'f_tag')).toBe('#${doc.fullName}')
    // Renaming something nobody reads changes nothing.
    const same = { ...s, docSchema: rename(s.docSchema, 'f_tag', 'label') }
    expect(followDesign(s, same)).toBe(same)
  })

  it('OC-139 AC2: a move rewrites relative paths, to doc when self no longer fits', () => {
    const s = setComputed(base(), 'f_label', { lang: 'template', expr: '${self.sku}-${parent.lastName}' })
    // sku moves out of the items to the root.
    const next = followDesign(s, { ...s, docSchema: move(s.docSchema, 'f_sku', 'fld_root', 0) })
    expect(exprOf(next, 'f_label')).toBe('${doc.sku}-${parent.lastName}')
  })

  it('OC-139 AC3: a removed field stays as written (validation flags it)', () => {
    const s = setComputed(base(), 'f_full', { lang: 'template', expr: '${doc.firstName} ${doc.lastName}' })
    const next = followDesign(s, deleteNode(s, 'f_last'))
    expect(exprOf(next, 'f_full')).toBe('${doc.firstName} ${doc.lastName}')
  })

  it('OC-139 AC4: uses and used by', () => {
    let s = setComputed(base(), 'f_full', { lang: 'template', expr: '${doc.firstName} ${doc.lastName}' })
    s = setComputed(s, 'f_tag', { lang: 'template', expr: '#${doc.fullName}' })
    expect(usesAndUsedBy(s, 'f_full')).toEqual({ uses: ['f_first', 'f_last'], usedBy: ['f_tag'] })
    expect(usesAndUsedBy(s, 'f_first')).toEqual({ uses: [], usedBy: ['f_full'] })
  })
})
