import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { readFileSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { MappingSpec } from '@/features/mapping/types'
import { mockComposer, mockOrdersMapping } from '@/test/composer'
import { mappingView, mockMapping, sampleSpec } from '@/test/mapping'
import { server, setupMswServer } from '@/test/msw'
import { makeProject, mockProject, repo } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'
import { DND_GROUP, DND_TABLE } from './DocumentTree'

setupMswServer()
beforeEach(() => {
  window.localStorage.clear()
  mockSession('owner')
  mockProject(makeProject({ source: repo('postgres'), target: repo('couchbase'), mappings: 1, readyMappings: 0 }))
  mockComposer()
  server.use(http.get('*/api/v1/projects/:pid/mappings', () => HttpResponse.json({ items: [] })))
})

// Also compiled by the server: backend/internal/plan/compile_test.go (TestCompile_ComposerGolden).
const GOLDEN = resolve(process.cwd(), 'testdata/composer/order_from_blank.json')

/** Random ids (fields, relations, mappings) become fld_1, rel_1, map_1… in order of appearance. */
export function normalizeIds(spec: MappingSpec): unknown {
  const text = JSON.stringify(spec)
  const seen = new Map<string, string>()
  const counts: Record<string, number> = {}
  return JSON.parse(
    text.replace(/"((fld|rel|map)_[A-Za-z0-9]{8,10})"/g, (_m, id: string, prefix: string) => {
      if (id === 'fld_root') return `"${id}"`
      if (!seen.has(id)) {
        counts[prefix] = (counts[prefix] ?? 0) + 1
        seen.set(id, `${prefix}_${counts[prefix]}`)
      }
      return `"${seen.get(id)}"`
    }),
  )
}

/** The orders mapping, still on the canvas. */
function openOrders() {
  const state = mockOrdersMapping()
  renderRoutes(routes, '/projects/p1/mappings/m1')
  return state
}

const tree = () => screen.getByRole('tree', { name: 'Document' })
const node = (name: RegExp) => within(tree()).getByRole('treeitem', { name })

/** Drops a table from the Sources pane on a node (jsdom has no DataTransfer). */
function dropTable(target: HTMLElement, name: string) {
  const data = { types: [DND_TABLE], getData: (t: string) => (t === DND_TABLE ? JSON.stringify({ schema: 'public', name }) : '') }
  fireEvent.dragOver(target, { dataTransfer: data })
  fireEvent.drop(target, { dataTransfer: data })
}

async function renameNew(name: string) {
  const input = await screen.findByRole('textbox', { name: /^Rename / })
  await userEvent.clear(input)
  await userEvent.type(input, `${name}{Enter}`)
}

async function menu(nodeName: string, action: string) {
  await userEvent.click(screen.getByRole('button', { name: `Actions for ${nodeName}` }))
  await userEvent.click(await screen.findByRole('menuitem', { name: action }))
}

async function bindWith(option: RegExp) {
  const dialog = await screen.findByRole('dialog')
  expect(await within(dialog).findByRole('radio', { name: option })).toBeChecked()
  const gen = within(dialog).getByRole('checkbox', { name: 'Generate item fields' })
  if ((gen as HTMLInputElement).checked) await userEvent.click(gen)
  await userEvent.click(within(dialog).getByRole('checkbox', { name: 'Auto-map' }))
  await userEvent.click(within(dialog).getByRole('button', { name: 'Bind' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
}

async function mapFrom(field: RegExp, option: RegExp) {
  await userEvent.click(node(field))
  const source = await screen.findByRole('combobox', { name: /^Source for / })
  await within(source).findByRole('option', { name: option })
  await userEvent.selectOptions(source, within(source).getByRole('option', { name: option }))
}

describe('Composer', () => {
  it('AC1: builds order document from blank', { timeout: 30_000 }, async () => {
    const state = openOrders()
    await userEvent.click(await screen.findByRole('tab', { name: 'Composer' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Start over' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: 'Blank' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Replace' }))

    // orderId: Tab on the root adds a child field.
    await userEvent.click(node(/^\$/))
    await userEvent.keyboard('{Tab}')
    await renameNew('orderId')
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Type' }), 'integer')

    // items[] with quantity and a product object holding sku.
    await menu('$', 'Add array')
    await renameNew('items')
    await menu('items', 'Add field')
    await renameNew('quantity')
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Type' }), 'integer')
    await menu('items', 'Add object')
    await renameNew('product')
    await menu('product', 'Add field')
    await renameNew('sku')

    // Bind by drag: items ← order_items (one-to-many), product ← products (many-to-one under items).
    dropTable(node(/^items, array/), 'order_items')
    await bindWith(/^One-to-many: order_id = order_id/)
    dropTable(node(/^product, object/), 'products')
    await bindWith(/^Many-to-one: product_id = product_id/)
    expect(node(/^items, array, bound to order_items/)).toBeInTheDocument()
    expect(node(/^product, object, bound to products/)).toBeInTheDocument()

    // Map the fields from in-scope columns.
    await mapFrom(/^orderId,/, /^order_id/)
    await mapFrom(/^quantity,/, /^quantity/)
    await mapFrom(/^sku,/, /^sku/)
    expect(node(/^sku, string, from products\.sku/)).toBeInTheDocument()

    // Autosave: the saved spec is the golden order document.
    await waitFor(() => expect(JSON.stringify(state.puts.at(-1)?.body)).toContain('"targetFieldId"'), { timeout: 5000 })
    await waitFor(() => expect((state.puts.at(-1)!.body as { spec: MappingSpec }).spec.fieldMappings).toHaveLength(3), { timeout: 5000 })
    const saved = normalizeIds((state.puts.at(-1)!.body as { spec: MappingSpec }).spec)
    if (process.env.UPDATE_GOLDEN) writeFileSync(GOLDEN, JSON.stringify(saved, null, 2) + '\n')
    expect(saved).toEqual(JSON.parse(readFileSync(GOLDEN, 'utf8')))
  })

  it('AC2: infers tree from pasted JSON', async () => {
    // Inference is the server's (OC-059); this is what it returns for the sample below.
    server.use(
      http.post('*/api/v1/doc-schemas/infer', () =>
        HttpResponse.json({
          warnings: [],
          docSchema: {
            source: 'json-sample', reference: { orderId: 1, items: [{ sku: 'A', qty: 2 }] },
            imported: { at: '2026-09-30T00:00:00Z', format: 'sample', samples: 1, fieldCount: 4 },
            root: { id: 'fld_root', name: '$', type: 'object', children: [
              { id: 'fld_a', name: 'orderId', type: 'integer', required: true, example: 1 },
              { id: 'fld_b', name: 'items', type: 'array', required: true, items: { id: 'fld_c', name: '[]', type: 'object', children: [{ id: 'fld_d', name: 'sku', type: 'string', example: 'A' }, { id: 'fld_e', name: 'qty', type: 'integer', example: 2 }] } },
            ] },
          },
        }),
      ),
    )
    openOrders()
    await userEvent.click(await screen.findByRole('tab', { name: 'Composer' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Import JSON design…' }))
    const box = await screen.findByRole('textbox', { name: 'JSON design' })
    await userEvent.click(box)
    await userEvent.paste('{"orderId":1,"items":[{"sku":"A","qty":2}]}')
    await userEvent.click(screen.getByRole('button', { name: 'Read design' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Replace design' }))
    await waitFor(() => expect(node(/^orderId, integer$/)).toBeInTheDocument())
    expect(node(/^items, array$/)).toBeInTheDocument()
    expect(node(/^sku, string$/)).toBeInTheDocument()
    expect(node(/^qty, integer$/)).toBeInTheDocument()
    // Nothing is bound or mapped: no "bound to" / "from" in any node's name.
    for (const item of within(tree()).getAllByRole('treeitem')) expect(item).not.toHaveAccessibleName(/bound to|from /)
    // The customer's document shows beside the generated one.
    expect(await screen.findByRole('region', { name: 'Reference' })).toHaveTextContent('"qty": 2')
    // What's left to bind, with the way to bind it (OC-060).
    expect(screen.getByText('Not bound to a table yet: 1 array.')).toBeInTheDocument()
    expect(screen.getByText('1 unbound')).toBeInTheDocument()
    server.use(http.post('*/api/v1/projects/:pid/mappings/:mid/suggest-bindings', () => HttpResponse.json({ root: [], suggestions: [] })))
    await userEvent.click(screen.getAllByRole('button', { name: 'Suggest bindings' })[1]!)
    const suggest = await screen.findByRole('dialog', { name: 'Suggested bindings' })
    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(suggest).not.toBeInTheDocument())

    // Strict conformance to the imported design (OC-061).
    const strict = screen.getByRole('checkbox', { name: 'Strict' })
    await userEvent.click(strict)
    expect(strict).toBeChecked()

    // The mapping sheet downloads through the API.
    const sheets: string[] = []
    server.use(
      http.get('*/api/v1/projects/:pid/mappings/:mid/mapping-sheet', ({ request }) => {
        sheets.push(new URL(request.url).searchParams.get('format')!)
        return new HttpResponse('path,type\n', { headers: { 'Content-Type': 'text/csv', 'Content-Disposition': 'attachment; filename="public-orders-mapping-sheet-2026-09-30.csv"' } })
      }),
    )
    const created = URL.createObjectURL
    URL.createObjectURL = () => 'blob:sheet'
    URL.revokeObjectURL = () => {}
    try {
      await userEvent.click(screen.getByRole('button', { name: 'Mapping sheet' }))
      await userEvent.click(await screen.findByRole('menuitem', { name: 'Mapping sheet (CSV)' }))
      expect(await screen.findByText('Downloaded public-orders-mapping-sheet-2026-09-30.csv.')).toBeInTheDocument()
      expect(sheets).toEqual(['csv'])
    } finally {
      URL.createObjectURL = created
    }
  })

  it('AC6: group the driving table', async () => {
    // The customer's case (OC-063): the detail table alone — one row per order line — and one
    // document per order with its lines in products[].
    const spec = sampleSpec()
    spec.docSchema.root.children = [
      { id: 'f_oid', name: 'orderId', type: 'integer' },
      { id: 'f_products', name: 'products', type: 'array', items: { id: 'f_item', name: '[]', type: 'object', children: [{ id: 'f_line', name: 'lineNo', type: 'integer' }, { id: 'f_qty', name: 'quantity', type: 'integer' }] } },
    ]
    spec.fieldMappings = [{ id: 'm_oid', targetFieldId: 'f_oid', sources: [{ column: 'order_id' }], transforms: [], origin: 'manual', state: 'active' }]
    spec.key = { template: 'order::{order_id}' }
    const state = mockMapping(mappingView({ sourceSchema: 'public', sourceTable: 'order_items', spec: { ...spec, mode: 'composed', root: { table: { schema: 'public', name: 'order_items' } } }, embedTables: {}, embedCandidates: [], issues: [] }))
    renderRoutes(routes, '/projects/p1/mappings/m1')
    expect(await screen.findByRole('tab', { name: 'Composer' })).toHaveAttribute('aria-selected', 'true')

    // One document per distinct order_id.
    await userEvent.click(await screen.findByRole('button', { name: 'One document per: row' }))
    await userEvent.click(screen.getByRole('radio', { name: 'Each distinct value of:' }))
    await userEvent.click(within(screen.getByRole('list', { name: 'Each distinct value of:' })).getByRole('checkbox', { name: /^order_id/ }))
    await userEvent.click(screen.getByRole('button', { name: 'Apply' }))
    expect(await screen.findByRole('button', { name: 'One document per: distinct order_id' })).toBeInTheDocument()

    // Drop "Rows of this group" on products[]: its lines, in line_no order.
    const data = { types: [DND_GROUP], getData: (t: string) => (t === DND_GROUP ? '1' : '') }
    fireEvent.dragOver(node(/^products, array/), { dataTransfer: data })
    fireEvent.drop(node(/^products, array/), { dataTransfer: data })
    await waitFor(() => expect(node(/^products, array, bound to order_items/)).toHaveTextContent('group rows'))
    const fromGroup = async (field: RegExp, column: RegExp) => {
      await userEvent.click(node(field))
      const source = await screen.findByRole('combobox', { name: /^Source for / })
      const rows = await within(source).findByRole('group', { name: 'Rows of this group (order_items)' })
      await userEvent.selectOptions(source, within(rows).getByRole('option', { name: column }))
    }
    await fromGroup(/^lineNo,/, /^line_no/)
    await fromGroup(/^quantity,/, /^quantity/)

    await waitFor(() => expect((state.puts.at(-1)?.body as { spec: MappingSpec } | undefined)?.spec.fieldMappings).toHaveLength(3), { timeout: 5000 })
    const saved = (state.puts.at(-1)!.body as { spec: MappingSpec }).spec
    expect(saved.root).toEqual({ table: { schema: 'public', name: 'order_items' }, groupBy: ['order_id'] })
    const group = saved.relations.find((r) => r.group)!
    expect(group).toMatchObject({ targetFieldId: 'f_products', kind: 'one-to-many', table: { schema: 'public', name: 'order_items' }, join: [{ parent: 'order_id', child: 'order_id' }], orderBy: [{ column: 'line_no' }], parentId: null })
    expect(saved.fieldMappings.find((m) => m.targetFieldId === 'f_line')!.sources).toEqual([{ relation: group.id, column: 'line_no' }])

    // Back to one document per row: the group's rows are unbound.
    await userEvent.click(screen.getByRole('button', { name: 'One document per: distinct order_id' }))
    await userEvent.click(screen.getByRole('radio', { name: 'Each row of order_items' }))
    await userEvent.click(screen.getByRole('button', { name: 'Apply' }))
    await waitFor(() => expect(node(/^products, array$/)).toBeInTheDocument())
  })

  it('OC-064 AC1: embed products here', async () => {
    // The grouped order_detail document with productId in each line: replace it with the product.
    const spec = sampleSpec()
    spec.docSchema.root.children = [
      { id: 'f_oid', name: 'orderId', type: 'integer' },
      { id: 'f_products', name: 'products', type: 'array', binding: { relationId: 'rel_lines' }, items: { id: 'f_item', name: '[]', type: 'object', children: [{ id: 'f_line', name: 'lineNo', type: 'integer' }, { id: 'f_pid', name: 'productId', type: 'integer' }] } },
    ]
    const lines = { schema: 'public', name: 'order_items' }
    spec.relations = [{ id: 'rel_lines', parentId: null, group: true, kind: 'one-to-many', table: lines, join: [{ parent: 'order_id', child: 'order_id' }], targetFieldId: 'f_products', orderBy: [{ column: 'line_no' }] }]
    spec.fieldMappings = [
      { id: 'm_oid', targetFieldId: 'f_oid', sources: [{ column: 'order_id' }], transforms: [], origin: 'manual', state: 'active' },
      { id: 'm_line', targetFieldId: 'f_line', sources: [{ relation: 'rel_lines', column: 'line_no' }], transforms: [], origin: 'manual', state: 'active' },
      { id: 'm_pid', targetFieldId: 'f_pid', sources: [{ relation: 'rel_lines', column: 'product_id' }], transforms: [], origin: 'manual', state: 'active' },
    ]
    spec.key = { template: 'order::{order_id}' }
    const state = mockMapping(mappingView({ sourceSchema: 'public', sourceTable: 'order_items', spec: { ...spec, mode: 'composed', root: { table: lines, groupBy: ['order_id'] } }, embedTables: {}, embedCandidates: [], issues: [] }))
    const automaps: unknown[] = []
    server.use(http.post('*/api/v1/projects/:pid/mappings/:mid/automap', async ({ request }) => (automaps.push(await request.json()), HttpResponse.json(state.view))))
    renderRoutes(routes, '/projects/p1/mappings/m1')
    await screen.findByRole('tab', { name: 'Composer', selected: true })

    // lineNo isn't a key: no embed; productId is.
    await userEvent.click(screen.getByRole('button', { name: 'Actions for lineNo' }))
    expect(screen.queryByRole('menuitem', { name: /^Embed / })).not.toBeInTheDocument()
    await userEvent.keyboard('{Escape}')
    await menu('productId', 'Embed products here…')
    const dialog = await screen.findByRole('dialog', { name: 'Embed products' })
    expect(within(dialog).getByRole('radio', { name: 'Replace productId with the object' })).toBeChecked()
    expect(within(dialog).getByRole('textbox', { name: 'Object name' })).toHaveValue('product')
    expect(await within(dialog).findByRole('checkbox', { name: 'sku' })).toBeChecked()
    // OC-082 AC2: replacing keeps the key in the object; keeping the field alongside leaves it out.
    expect(within(dialog).getByRole('checkbox', { name: 'product_id' })).toBeChecked()
    await userEvent.click(within(dialog).getByRole('radio', { name: 'Keep productId and add the object after it' }))
    expect(within(dialog).getByRole('checkbox', { name: 'product_id' })).not.toBeChecked()
    await userEvent.click(within(dialog).getByRole('radio', { name: 'Replace productId with the object' }))
    expect(within(dialog).getByRole('checkbox', { name: 'product_id' })).toBeChecked()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Embed' }))

    await waitFor(() => expect(node(/^product, object, bound to products/)).toBeInTheDocument())
    expect(within(tree()).getAllByRole('treeitem', { name: /^productId,/ }), 'only the object\'s own key').toHaveLength(1)
    await waitFor(() => expect(automaps.at(-1)).toMatchObject({ nodeId: expect.stringMatching(/^fld_/) }))

    // And back.
    await menu('product', 'Collapse to id')
    await waitFor(() => expect(node(/^productId, integer, from order_items\.product_id/)).toBeInTheDocument())
  })

  it('OC-071 AC3: issue icons, keep the key, reference fixes', async () => {
    // The customer's design has products[].productId and products[].quantity; quantity is gone.
    const spec = sampleSpec()
    spec.docSchema.root.children = [
      { id: 'f_oid', name: 'orderId', type: 'integer' },
      { id: 'f_products', name: 'products', type: 'array', binding: { relationId: 'rel_lines' }, items: { id: 'f_item', name: '[]', type: 'object', children: [{ id: 'f_line', name: 'lineNo', type: 'integer' }, { id: 'f_pid', name: 'productId', type: 'integer' }] } },
    ]
    spec.docSchema.imported = {
      at: '2026-10-01T00:00:00Z', format: 'sample', samples: 1, fieldCount: 6,
      fields: [{ path: 'orderId', type: 'integer' }, { path: 'products', type: 'array' }, { path: 'products[]', type: 'object' }, { path: 'products[].lineNo', type: 'integer' }, { path: 'products[].productId', type: 'integer' }, { path: 'products[].quantity', type: 'integer' }],
    }
    const lines = { schema: 'public', name: 'order_items' }
    spec.relations = [{ id: 'rel_lines', parentId: null, group: true, kind: 'one-to-many', table: lines, join: [{ parent: 'order_id', child: 'order_id' }], targetFieldId: 'f_products' }]
    spec.fieldMappings = [
      { id: 'm_line', targetFieldId: 'f_line', sources: [{ relation: 'rel_lines', column: 'line_no' }], transforms: [], origin: 'manual', state: 'active' },
      { id: 'm_pid', targetFieldId: 'f_pid', sources: [{ relation: 'rel_lines', column: 'product_id' }], transforms: [], origin: 'manual', state: 'active' },
    ]
    spec.key = { template: 'order::{order_id}' }
    const message = 'products[].quantity is in the design you imported but missing here; it may have been replaced when an object was embedded. Add it back.'
    const issues = [{ severity: 'warning' as const, code: 'REFERENCE_FIELD_UNMAPPED', path: 'products[].quantity', nodePath: 'products[].quantity', message, fix: { kind: 'restore-field' as const, label: 'Add quantity back (from quantity)', relationId: 'rel_lines', columns: ['quantity'] } }]
    mockMapping(mappingView({ sourceSchema: 'public', sourceTable: 'order_items', spec: { ...spec, mode: 'composed', root: { table: lines, groupBy: ['order_id'] } }, embedTables: {}, embedCandidates: [], issues }))
    renderRoutes(routes, '/projects/p1/mappings/m1')
    await screen.findByRole('tab', { name: 'Composer', selected: true })

    // The missing field's warning sits in front of its parent, the item, with the message.
    const item = node(/each item, object, 1 warning/)
    expect(within(item).getByRole('img', { name: message })).toBeInTheDocument()
    // The problems panel offers the fix.
    expect(screen.getByRole('button', { name: 'Add quantity back (from quantity)' })).toBeInTheDocument()

    // productId is in the customer's design: Embed still replaces it by default (OC-082), and says so.
    await menu('productId', 'Embed products here…')
    const dialog = await screen.findByRole('dialog', { name: 'Embed products' })
    expect(within(dialog).getByRole('radio', { name: 'Replace productId with the object' })).toBeChecked()
    expect(dialog).toHaveTextContent('productId is in the design you imported: replacing it moves the key into the object')
  })

  it('opens a composed mapping in the composer, driven by its table', async () => {
    mockMapping(mappingView({
      sourceSchema: 'public', sourceTable: 'orders', embedTables: {}, embedCandidates: [], issues: [],
      spec: { ...sampleSpec(), mode: 'composed', root: { table: { schema: 'public', name: 'orders' } }, fieldMappings: [], ignoredColumns: [] },
    }))
    renderRoutes(routes, '/projects/p1/mappings/m1')
    expect(await screen.findByRole('tab', { name: 'Composer' })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('combobox', { name: 'Driving table' })).toHaveValue('public.orders')
  })
})
