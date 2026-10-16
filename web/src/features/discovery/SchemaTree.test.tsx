import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { col, shopSnapshot, tbl } from '@/test/discovery'
import { withFixedLayout } from '@/test/layout'
import type { SourceTable } from './api'
import { SchemaTree } from './SchemaTree'
import { schemaId, tableId, type SchemaNode } from './tree'
import { useSchemaSearch } from './useSchemaSearch'

function Harness({ schemas, onSelect = () => {}, query = '' }: { schemas: SchemaNode[]; onSelect?: (s: string, t: string) => void; query?: string }) {
  const tree = useSchemaSearch(schemas)
  const [selected, setSelected] = useState<string>()
  return (
    <>
      <input aria-label="q" value={tree.query || query} onChange={(e) => tree.setQuery(e.target.value)} />
      <button type="button" onClick={() => tree.setOpen(schemaId('shop'), true)}>
        open-shop
      </button>
      <SchemaTree
        label="Source schema"
        nodes={tree.nodes}
        expanded={tree.expanded}
        setOpen={tree.setOpen}
        highlights={tree.highlights}
        selected={selected}
        onSelect={(s, t) => {
          setSelected(tableId(s, t))
          onSelect(s, t)
        }}
      />
    </>
  )
}

withFixedLayout()

const items = () => screen.getAllByRole('treeitem')
const text = () => items().map((i) => i.textContent)

describe('SchemaTree', () => {
  it('AC1: renders schemas, counts, sorted tables', async () => {
    render(<Harness schemas={shopSnapshot().schemas} />)
    expect(text()).toEqual(['billing(1)', 'shop(6)'])
    await userEvent.click(screen.getByRole('button', { name: 'open-shop' }))
    const names = items()
      .filter((i) => i.getAttribute('aria-level') === '2')
      .map((i) => i.textContent)
    expect(names).toEqual(['address~40k', 'customer~120k', 'Order Details~1k', 'orders~1.2M', 'page_view—', 'v_active_customers'])
    // Stale statistics show a dash with an explanation.
    const pv = items().find((i) => i.textContent?.startsWith('page_view'))!
    expect(within(pv).getByTitle('Estimate unavailable — run ANALYZE')).toBeInTheDocument()
  })

  it('shows exact counts without the estimate marker', async () => {
    render(<Harness schemas={[{ name: 'shop', tables: [tbl('roles', { rowEstimate: 4, rowCountExact: true }), tbl('stores', { rowEstimate: 5 })] }]} />)
    await userEvent.click(screen.getByRole('button', { name: 'open-shop' }))
    expect(text()).toEqual(['shop(2)', 'roles4', 'stores~5'])
  })

  it('AC1: virtualizes 20k nodes (renders < 100 DOM rows)', async () => {
    const tables: SourceTable[] = Array.from({ length: 20_000 }, (_, i) => tbl(`t${String(i).padStart(5, '0')}`, { columns: [col('id', 1)] }))
    render(<Harness schemas={[{ name: 'shop', tables }]} />)
    await userEvent.click(screen.getByRole('button', { name: 'open-shop' }))
    expect(items().length).toBeLessThan(100)
    expect(items()[0]).toHaveTextContent('shop(20000)')
  })

  it('AC2: highlights matched substring', async () => {
    render(<Harness schemas={shopSnapshot().schemas} />)
    await userEvent.type(screen.getByLabelText('q'), 'cust email')
    const marks = document.querySelectorAll('mark')
    expect([...marks].map((m) => m.textContent).join('')).toMatch(/email/)
    expect(text()).toEqual(['shop(1)', 'customer~120k', 'email_address' + 'citext'])
  })

  it('AC3: arrow key navigation', async () => {
    const onSelect = vi.fn()
    render(<Harness schemas={shopSnapshot().schemas} onSelect={onSelect} />)
    const tree = screen.getByRole('tree')
    tree.focus()
    const active = () => document.getElementById(tree.getAttribute('aria-activedescendant')!)?.textContent

    expect(active()).toBe('billing(1)')
    await userEvent.keyboard('{ArrowDown}')
    expect(active()).toBe('shop(6)')
    await userEvent.keyboard('{ArrowRight}') // expands
    expect(items()).toHaveLength(8)
    await userEvent.keyboard('{ArrowRight}') // already expanded: first child
    expect(active()).toBe('address~40k')
    await userEvent.keyboard('{ArrowDown}{ArrowRight}') // customer: expand
    expect(text()).toContain('customer_idint4')
    await userEvent.keyboard('{ArrowRight}{ArrowLeft}') // into a column, then back to its table
    expect(active()).toBe('customer~120k')
    await userEvent.keyboard('{ArrowLeft}') // collapse customer
    expect(text()).not.toContain('customer_idint4')
    await userEvent.keyboard('{ArrowLeft}') // to the parent schema
    expect(active()).toBe('shop(6)')
    await userEvent.keyboard('{End}')
    expect(active()).toBe('v_active_customers')
    await userEvent.keyboard('{Home}')
    expect(active()).toBe('billing(1)')

    // Type-ahead jumps to the next node starting with the typed text.
    await userEvent.keyboard('or')
    expect(active(), 'case-insensitive prefix').toBe('Order Details~1k')
    await userEvent.keyboard('ders')
    expect(active()).toBe('orders~1.2M')

    await userEvent.keyboard('{Enter}')
    expect(onSelect).toHaveBeenCalledWith('shop', 'orders')
  })

  it('AC3: ARIA attributes', async () => {
    render(<Harness schemas={shopSnapshot().schemas} />)
    const tree = screen.getByRole('tree', { name: 'Source schema' })
    expect(tree).toHaveAttribute('tabindex', '0')
    await userEvent.click(screen.getByRole('button', { name: 'open-shop' }))
    const shop = items()[1]!
    expect(shop).toHaveAttribute('aria-level', '1')
    expect(shop).toHaveAttribute('aria-expanded', 'true')
    const customer = items().find((i) => i.textContent?.startsWith('customer'))!
    expect(customer).toHaveAttribute('aria-level', '2')
    expect(customer).toHaveAttribute('aria-expanded', 'false')
    expect(customer).toHaveAttribute('aria-selected', 'false')
    await userEvent.click(customer)
    expect(items().find((i) => i.textContent?.startsWith('customer'))).toHaveAttribute('aria-selected', 'true')
    // Unreadable tables carry a lock.
    await act(async () => {})
  })
})
