import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import axe from 'axe-core'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { MappingSpec } from '@/features/mapping/types'
import { mockComposer, mockOrdersMapping } from '@/test/composer'
import { mockPOS, openCart } from '@/test/designFirst'
import { server, setupMswServer } from '@/test/msw'
import { makeProject, mockProject, repo } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'

setupMswServer()
beforeEach(() => {
  mockSession('owner')
  mockProject(makeProject({ source: repo('postgres'), target: repo('couchbase'), mappings: 1, readyMappings: 0 }))
  mockComposer()
  server.use(http.get('*/api/v1/projects/:pid/mappings', () => HttpResponse.json({ items: [] })))
})

const tree = () => screen.getByRole('tree', { name: 'Document' })
const focused = () => document.activeElement as HTMLElement

/** Presses a control that has keyboard focus (Tab reaches it; Enter activates it). */
async function press(el: HTMLElement) {
  el.focus()
  await userEvent.keyboard('{Enter}')
}

/** Opens the selected node's menu with Shift+F10 and picks an action with the arrows. */
async function menuAction(label: string) {
  await userEvent.keyboard('{Shift>}{F10}{/Shift}')
  await waitFor(() => expect(focused()).toHaveAttribute('role', 'menuitem'))
  for (let i = 0; i < 10 && focused().textContent !== label; i++) await userEvent.keyboard('{ArrowDown}')
  expect(focused()).toHaveTextContent(label)
  await userEvent.keyboard('{Enter}')
}

async function seriousViolations() {
  // Color contrast is verified from the tokens (tokens.test.ts); jsdom can't compute it.
  const res = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false }, region: { enabled: false } } })
  return res.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical').map((v) => `${v.id}: ${v.help} (${v.nodes.map((n) => n.target.join(' ')).join(', ')})`)
}

describe('Composer accessibility', () => {
  it('AC5: keyboard flow and axe', { timeout: 30_000 }, async () => {
    const state = mockOrdersMapping()
    renderRoutes(routes, '/projects/p1/mappings/m1')
    await press(await screen.findByRole('tab', { name: 'Composer' }))
    await press(await screen.findByRole('button', { name: 'Start over' }))
    await waitFor(() => expect(focused()).toHaveAttribute('role', 'menuitem'))
    expect(focused()).toHaveTextContent('Blank')
    await userEvent.keyboard('{Enter}')
    await press(await screen.findByRole('button', { name: 'Replace' }))

    // Tab on the root adds a field; type its name.
    within(tree()).getByRole('treeitem', { name: /^\$/ }).focus()
    await userEvent.keyboard('{Tab}')
    await waitFor(() => expect(focused()).toHaveAccessibleName(/^Rename /))
    await userEvent.keyboard('{Control>}a{/Control}orderId{Enter}')
    expect(within(tree()).getByRole('treeitem', { name: /^orderId/ })).toHaveAttribute('aria-selected', 'true')

    // Home → root; its menu adds an array (named items).
    await userEvent.keyboard('{Home}')
    await menuAction('Add array')
    await waitFor(() => expect(focused()).toHaveAccessibleName(/^Rename /))
    await userEvent.keyboard('{Enter}')

    // Bind items to order_items from its menu, without dragging.
    await waitFor(() => expect(within(tree()).getByRole('treeitem', { name: /^items, array/ })).toHaveAttribute('aria-selected', 'true'))
    within(tree()).getByRole('treeitem', { name: /^items, array/ }).focus()
    await menuAction('Bind to table…')
    const picker = await screen.findByRole('dialog', { name: 'Bind items to a table' })
    await userEvent.keyboard('order_items')
    await userEvent.keyboard('{Tab}')
    expect(focused()).toHaveTextContent('public.order_items')
    await userEvent.keyboard('{Enter}')
    await waitFor(() => expect(picker).not.toBeInTheDocument())
    const bind = await screen.findByRole('dialog', { name: 'Bind items to order_items' })
    expect(await within(bind).findByRole('radio', { name: /^One-to-many/ })).toBeChecked()
    await press(within(bind).getByRole('button', { name: 'Bind' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(within(tree()).getByRole('treeitem', { name: /^items, array, bound to order_items/ })).toBeInTheDocument()

    // Map orderId from a column: "Map from column…" moves focus to the Source picker.
    within(tree()).getByRole('treeitem', { selected: true }).focus()
    await userEvent.keyboard('{Home}{ArrowDown}')
    expect(within(tree()).getByRole('treeitem', { name: /^orderId/ })).toHaveAttribute('aria-selected', 'true')
    await menuAction('Map from column…')
    await waitFor(() => expect(focused()).toHaveAccessibleName('Source for orderId'))
    await userEvent.selectOptions(focused(), '|order_id|')

    // Saved (autosave), and no serious axe violations.
    await waitFor(() => expect((state.puts.at(-1)?.body as { spec: MappingSpec } | undefined)?.spec.fieldMappings.some((m) => m.sources[0]?.column === 'order_id')).toBe(true), { timeout: 5000 })
    expect(await screen.findByText('Saved · just now')).toBeInTheDocument()
    expect(await seriousViolations()).toEqual([])
  })

  it('AC7: import dialog', async () => {
    mockOrdersMapping()
    server.use(
      http.post('*/api/v1/doc-schemas/infer', () =>
        HttpResponse.json({ warnings: ['items has mixed types (integer, string); it will accept any value.'], docSchema: { source: 'json-sample', root: { id: 'fld_root', name: '$', type: 'object', children: [{ id: 'fld_a', name: 'orderId', type: 'integer', example: 1 }] } } }),
      ),
    )
    renderRoutes(routes, '/projects/p1/mappings/m1')
    await press(await screen.findByRole('tab', { name: 'Composer' }))
    await press(await screen.findByRole('button', { name: 'Import JSON design…' }))
    const dialog = await screen.findByRole('dialog', { name: 'Import a JSON design' })
    // The drop zone has a button equivalent, reachable with Tab.
    expect(focused()).toHaveAccessibleName('JSON design')
    await userEvent.keyboard('{Tab}')
    expect(focused()).toHaveAccessibleName('Choose file…')
    within(dialog).getByRole('textbox', { name: 'JSON design' }).focus()
    await userEvent.paste('{"orderId":1}')
    expect(await seriousViolations()).toEqual([])
    await press(within(dialog).getByRole('button', { name: 'Read design' }))
    // The report is announced.
    const report = await within(dialog).findByRole('status', { name: 'What will be imported' })
    expect(report).toHaveAttribute('aria-live', 'polite')
    expect(report).toHaveTextContent('mixed types')
    expect(await seriousViolations()).toEqual([])
  })

  it('OC-168 AC4: review mode', async () => {
    mockPOS()
    openCart()
    renderRoutes(routes, '/projects/p1/mappings/m1')
    await userEvent.click(await screen.findByRole('button', { name: 'Propose mapping' }, { timeout: 8000 }))
    const banner = await screen.findByRole('region', { name: 'Proposed mapping' }, { timeout: 8000 })
    expect(within(banner).getByRole('status')).toHaveTextContent('need review')
    // The chips have text: the confidence is in each row's name.
    expect(within(tree()).getAllByRole('treeitem', { name: /high confidence/ }).length).toBeGreaterThan(10)
    await userEvent.click(within(banner).getByRole('radio', { name: /Needs review/ }))
    expect(within(banner).getByRole('radio', { name: /Needs review/ })).toHaveAttribute('aria-checked', 'true')
    expect(await seriousViolations()).toEqual([])
  }, 20_000)
})

