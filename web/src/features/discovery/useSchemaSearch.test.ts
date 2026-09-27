import { act, renderHook } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { shopSnapshot } from '@/test/discovery'
import { columnId, schemaId, tableId } from './tree'
import { useSchemaSearch } from './useSchemaSearch'

const schemas = shopSnapshot().schemas

const labels = (nodes: { kind: string; name: string }[]) => nodes.map((n) => `${n.kind}:${n.name}`)

describe('useSchemaSearch', () => {
  it('AC2: matches column path and returns expand set', () => {
    const { result } = renderHook(() => useSchemaSearch(schemas))
    act(() => result.current.setQuery('cust email'))
    const { nodes, expanded, highlights } = result.current
    expect(expanded.has(schemaId('shop'))).toBe(true)
    expect(expanded.has(tableId('shop', 'customer'))).toBe(true)
    expect(labels(nodes)).toEqual(['schema:shop', 'table:customer', 'column:email_address'])
    // "email" is highlighted inside the column label.
    const ranges = highlights.get(columnId('shop', 'customer', 'email_address'))
    expect(ranges).toBeDefined()
    expect(ranges!.some(([a, b]) => 'email_address'.slice(a, b + 1).includes('email'))).toBe(true)
  })

  it('AC2: a table-name match shows the table with all its columns once expanded', () => {
    const { result } = renderHook(() => useSchemaSearch(schemas))
    act(() => result.current.setQuery('orders'))
    expect(labels(result.current.nodes)).toContain('table:orders')
    expect(result.current.nodes.find((n) => n.kind === 'column' && n.table === 'orders')).toBeUndefined()
    act(() => result.current.setOpen(tableId('shop', 'orders'), true))
    expect(labels(result.current.nodes)).toEqual(expect.arrayContaining(['column:order_id', 'column:customer_id']))
  })

  it('AC2: filter chips combine with AND', () => {
    const { result } = renderHook(() => useSchemaSearch(schemas))
    act(() => result.current.setOpen(schemaId('shop'), true))
    act(() => result.current.toggleChip('noPk'))
    expect(labels(result.current.nodes)).toEqual(['schema:shop', 'table:page_view', 'table:v_active_customers'])
    act(() => result.current.toggleChip('tables'))
    expect(labels(result.current.nodes)).toEqual(['schema:shop', 'table:page_view'])
    act(() => result.current.setQuery('customer'))
    expect(result.current.nodes, 'no table matches every filter').toEqual([])
    act(() => result.current.toggleChip('noPk'))
    act(() => result.current.toggleChip('tables'))
    expect(labels(result.current.nodes)).toContain('table:customer')
  })

  it('collapsing during a search sticks until the query changes', () => {
    const { result } = renderHook(() => useSchemaSearch(schemas))
    act(() => result.current.setQuery('cust email'))
    act(() => result.current.setOpen(tableId('shop', 'customer'), false))
    expect(labels(result.current.nodes)).toEqual(['schema:shop', 'table:customer'])
    act(() => result.current.setQuery('cust emai'))
    expect(labels(result.current.nodes)).toContain('column:email_address')
    act(() => result.current.setQuery(''))
    expect(labels(result.current.nodes), 'sorted, collapsed without a query').toEqual(['schema:billing', 'schema:shop'])
  })
})
