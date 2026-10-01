import { expect, test, type Page } from '@playwright/test'
import { Api } from './support/api'
import { SignIn } from './support/pages'

// OC-059/060 (E2E): import the customer's order document into the composer against the real
// backend, accept the suggested bindings — items[] ← sales.order_items, items[].product ←
// sales.products, tags[] ← sales.tags through order_tags — auto-map, and the sample shows real
// orders in the customer's shape.

const tree = (page: Page) => page.getByRole('tree', { name: 'Document' })
const node = (page: Page, name: RegExp) => tree(page).getByRole('treeitem', { name })

const customerDesign = JSON.stringify({
  orderId: 1001,
  status: 'paid',
  orderedAt: '2024-03-01T10:00:00Z',
  items: [{ lineNo: 1, quantity: 2, unitPrice: 9.5, product: { sku: 'SKU-1', name: 'Kettle' } }],
  tags: ['gift'],
})

test('import, suggest, preview', async ({ page }) => {
  const api = await Api.signUp()
  const p = await api.projectWithRepos()
  const { ids } = await api.mappings(p.id, ['sales.orders'])
  await new SignIn(page).signIn(api.email)
  await page.goto(`/projects/${p.id}/mappings/${ids[0]}`)
  await page.getByRole('tab', { name: 'Composer' }).click()

  // Import the customer's document from a file.
  await page.getByRole('button', { name: 'Import JSON design…' }).click()
  const dialog = page.getByRole('dialog', { name: 'Import a JSON design' })
  await dialog.getByLabel('JSON or NDJSON file').setInputFiles({ name: 'order.json', mimeType: 'application/json', buffer: Buffer.from(customerDesign) })
  await expect(dialog.getByText('order.json')).toBeVisible()
  await dialog.getByRole('button', { name: 'Read design' }).click()
  await expect(dialog.getByRole('status', { name: 'What will be imported' })).toContainText('11 fields')
  await dialog.getByRole('button', { name: 'Replace design' }).click()
  await expect(node(page, /^orderedAt, datetime$/)).toBeVisible()
  await expect(node(page, /^unitPrice, number$/)).toBeVisible()

  // Suggest bindings from the banner; accept the high-confidence ones and auto-map.
  await expect(page.getByText(/^Not bound to a table yet: 2 arrays and 1 object\.$/)).toBeVisible()
  await page.getByRole('status').filter({ hasText: 'Not bound to a table yet' }).getByRole('button', { name: 'Suggest bindings' }).click()
  const suggest = page.getByRole('dialog', { name: 'Suggested bindings' })
  const list = suggest.getByRole('list', { name: 'Suggestions' })
  await expect(list.getByRole('listitem', { name: 'items[]', exact: true })).toContainText('sales.order_items · one-to-many')
  await expect(list.getByRole('listitem', { name: 'items[].product' })).toContainText('sales.products · many-to-one')
  await expect(list.getByRole('listitem', { name: 'tags[]', exact: true })).toContainText('sales.tags · many-to-many')
  await suggest.getByRole('button', { name: 'Accept high & auto-map' }).click()
  await expect(suggest).toBeHidden()
  await expect(node(page, /^items, array, bound to order_items/)).toBeVisible()
  await expect(node(page, /^product, object, bound to products/)).toBeVisible()
  await expect(node(page, /^quantity, integer, from order_items\.quantity/)).toBeVisible({ timeout: 15_000 })
  await expect(node(page, /^sku, string, from products\.sku/)).toBeVisible()
  await expect(page.getByText('Saved · just now')).toBeVisible()

  // The sample: real orders in the customer's shape, next to their reference document.
  const sample = page.getByRole('region', { name: 'Sample' })
  await expect(sample.getByText(/"unitPrice"/).first()).toBeVisible({ timeout: 30_000 })
  await expect(sample.getByRole('region', { name: 'Reference' })).toContainText('"Kettle"')

  // Saved: composed, three bindings, product under items, the reference kept.
  const saved = await api.get<{ spec: { relations: { id: string; table: { name: string }; parentId: string | null }[]; docSchema: { reference?: unknown; imported?: { fileName?: string } } } }>(
    `/api/v1/projects/${p.id}/mappings/${ids[0]}`,
  )
  expect(saved.spec.relations.map((r) => r.table.name).sort()).toEqual(['order_items', 'products', 'tags'])
  const items = saved.spec.relations.find((r) => r.table.name === 'order_items')!
  expect(saved.spec.relations.find((r) => r.table.name === 'products')!.parentId).toBe(items.id)
  expect(saved.spec.docSchema.imported?.fileName).toBe('order.json')
  expect(saved.spec.docSchema.reference).toMatchObject({ orderId: 1001 })

  // OC-061: the customer revises the design — unitPrice → unit_price, lineNo dropped, a
  // currency added. Updating keeps every mapping of the fields that still exist.
  const revised = JSON.stringify({ ...JSON.parse(customerDesign), currency: 'EUR', items: [{ quantity: 2, unit_price: 9.5, product: { sku: 'SKU-1', name: 'Kettle' } }] })
  await page.getByRole('button', { name: 'Import JSON design…' }).click()
  const again = page.getByRole('dialog', { name: 'Import a JSON design' })
  await again.getByLabel('JSON or NDJSON file').setInputFiles({ name: 'order-v2.json', mimeType: 'application/json', buffer: Buffer.from(revised) })
  await again.getByRole('button', { name: 'Read design' }).click()
  await again.getByRole('button', { name: 'Update design, keep mappings' }).click()
  await expect(again.getByRole('checkbox', { name: 'items[].unitPrice renamed to items[].unit_price' })).toBeChecked()
  await expect(again.getByRole('list', { name: '1 field removed' })).toContainText('items[].lineNo')
  await again.getByRole('checkbox', { name: /^Remove 1 fields with their 1 mappings/ }).check()
  await again.getByRole('button', { name: 'Update design' }).click()
  await expect(again).toBeHidden()
  await expect(node(page, /^unit_price, number, from order_items\.unit_price/)).toBeVisible()
  await expect(node(page, /^quantity, integer, from order_items\.quantity/)).toBeVisible()
  await expect(node(page, /^currency, string$/)).toBeVisible()

  // The mapping sheet (of the saved mapping, once autosave has it) lists every field with
  // what fills it.
  const sheetUrl = `/api/v1/projects/${p.id}/mappings/${ids[0]}/mapping-sheet?format=csv`
  await expect.poll(() => api.getText(sheetUrl), { timeout: 10_000 }).toContain('items[].unit_price,number')
  const sheet = await api.getText(sheetUrl)
  expect(sheet.split('\n')[0]).toBe('path,type,required,example,source,transform,constant,status,issues')
  expect(sheet).not.toContain('lineNo')
  expect(sheet).toMatch(/items\[\]\.quantity,integer,yes,2,order_items\.quantity,,,mapped/)

  await api.dispose()
})
