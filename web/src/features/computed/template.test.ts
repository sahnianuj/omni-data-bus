import { describe, expect, it } from 'vitest'
import { asTemplate } from './template'

describe('asTemplate', () => {
  it('turns quoted text joined with + into the template it means', () => {
    expect(asTemplate('"paid_in_out::" + ${doc.id}')).toBe('paid_in_out::${doc.id}')
    expect(asTemplate('"paid_in_out::" + doc.id')).toBe('paid_in_out::${doc.id}')
    expect(asTemplate("'SO-' + ${row.order_no} + '/' + ${self.line}")).toBe('SO-${row.order_no}/${self.line}')
    expect(asTemplate('"a + b" + ${doc.x}')).toBe('a + b${doc.x}')
  })

  it('leaves real templates alone', () => {
    expect(asTemplate('paid_in_out::${doc.id}')).toBeNull()
    expect(asTemplate('${doc.a} + ${doc.b}')).toBeNull() // no quotes: maybe meant as written
    expect(asTemplate('"x" + 1')).toBeNull()
  })
})
