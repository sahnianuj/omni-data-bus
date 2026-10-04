import { expect, test, type Page } from '@playwright/test'
import { Api } from './support/api'
import { SignIn } from './support/pages'

// OC-063 AC6 (E2E): the customer's case against the real backend — sales.order_detail holds one
// row per order line and has no orders table. Importing their order document, the suggestion
// groups order_detail by order_id and fills products[] with each order's lines; the sample is
// one document per order.

const tree = (page: Page) => page.getByRole('tree', { name: 'Document' })
const node = (page: Page, name: RegExp) => tree(page).getByRole('treeitem', { name })

const customerDesign = JSON.stringify({
  type: 'order_detail',
  orderId: 1,
  products: [
    { lineNo: 1, productId: 2, quantity: 3 },
    { lineNo: 2, productId: 4, quantity: 2 },
  ],
})

test('order_detail into one order', async ({ page }) => {
  const api = await Api.signUp()
  const p = await api.projectWithRepos()
  const { ids } = await api.mappings(p.id, ['sales.order_detail'])
  await new SignIn(page).signIn(api.email)
  await page.goto(`/projects/${p.id}/mappings/${ids[0]}`)
  await page.getByRole('tab', { name: 'Composer' }).click()

  await page.getByRole('button', { name: 'Import JSON design…' }).click()
  const dialog = page.getByRole('dialog', { name: 'Import a JSON design' })
  await dialog.getByRole('textbox', { name: 'JSON design' }).fill(customerDesign)
  await dialog.getByRole('button', { name: 'Read design' }).click()
  await expect(dialog.getByRole('status', { name: 'What will be imported' })).toContainText("The type field docType isn't in this design, so it's turned off")
  await dialog.getByRole('button', { name: 'Replace design' }).click()
  await expect(node(page, /^products, array$/)).toBeVisible()

  // The suggestion: group by order_id, products[] ← the group's rows in line order.
  await page.getByRole('status').filter({ hasText: 'Not bound to a table yet' }).getByRole('button', { name: 'Suggest bindings' }).click()
  const suggest = page.getByRole('dialog', { name: 'Suggested bindings' })
  const item = suggest.getByRole('listitem', { name: 'products[]', exact: true })
  await expect(item).toContainText('High')
  await expect(item).toContainText('one document per distinct order_id')
  await suggest.getByRole('button', { name: 'Accept high & auto-map' }).click()
  await expect(suggest).toBeHidden()

  await expect(page.getByRole('button', { name: 'One document per: distinct order_id' })).toBeVisible()
  await expect(node(page, /^products, array, bound to order_detail/)).toContainText('group rows')
  await expect(node(page, /^lineNo, integer, from order_detail\.line_no/)).toBeVisible({ timeout: 15_000 })
  await expect(node(page, /^quantity, integer, from order_detail\.quantity/)).toBeVisible()
  await expect(node(page, /^orderId, integer, from order_id/)).toBeVisible()

  // The sample: order 1 is one document with its 2 lines.
  const sample = page.getByRole('region', { name: 'Sample' })
  await expect(sample.getByRole('list', { name: 'Rows per node' })).toContainText('products', { timeout: 30_000 })
  const doc = sample.locator('pre').first()
  await expect(doc).toContainText('"orderId": 1')
  await expect(doc).toContainText('"lineNo": 2')
  await expect(doc).not.toContainText('"lineNo": 3')
  await expect(doc).not.toContainText('docType')
  // The imported design is shown next to the generated document (differences are listed only
  // in Strict mode, OC-076).
  await expect(sample.getByRole('region', { name: 'Reference' })).toContainText('"orderId": 1')

  // Saved: grouped, the key on order_id only.
  await expect.poll(async () => (await api.get<{ spec: { root?: { groupBy?: string[] } } }>(`/api/v1/projects/${p.id}/mappings/${ids[0]}`)).spec.root?.groupBy, { timeout: 10_000 }).toEqual(['order_id'])
  const saved = await api.get<{ spec: { key: { template: string }; relations: { group?: boolean; orderBy?: { column: string }[] }[] }; issues: { code: string; severity: string }[] }>(`/api/v1/projects/${p.id}/mappings/${ids[0]}`)
  expect(saved.spec.relations.find((r) => r.group)?.orderBy).toEqual([{ column: 'line_no' }])
  expect(saved.issues.filter((i) => i.severity === 'error').map((i) => i.code)).toEqual([])

  // OC-064: embed each line's product through product_id, in place of productId.
  await node(page, /^productId,/).click()
  await page.keyboard.press('Shift+F10')
  await page.getByRole('menuitem', { name: 'Embed products here…' }).click()
  const embed = page.getByRole('dialog', { name: 'Embed products' })
  await expect(embed.getByRole('checkbox', { name: 'sku' })).toBeChecked()
  await embed.getByRole('button', { name: 'Embed' }).click()
  await expect(embed).toBeHidden()
  await expect(node(page, /^product, object, bound to products/)).toBeVisible()
  await expect(node(page, /^sku, string, from products\.sku/)).toBeVisible({ timeout: 15_000 })
  await expect(sample.locator('pre').first()).toContainText('"sku": "SKU-', { timeout: 30_000 })

  // OC-062: the list says what the document is, and opens it in a chosen view.
  await page.goto(`/projects/${p.id}/mappings`)
  const row = page.getByRole('listitem').filter({ has: page.getByRole('link', { name: 'Open mapping for order_detail' }) })
  await expect(row.getByTestId('shape')).toContainText('nested: 1 array, 1 object · one per distinct order_id')
  await row.getByRole('button', { name: 'Open order_detail in…' }).click()
  await page.getByRole('menuitem', { name: 'Open in Design' }).click()
  await expect(page.getByRole('tab', { name: 'Design' })).toHaveAttribute('aria-selected', 'true')

  await api.dispose()
})

// OC-066 AC4: from the key collision the customer thinks in — key order::{order_id} on the flat
// order_detail mapping — to one document per order, every line once.
test('combine rows from the key', async ({ page }) => {
  const api = await Api.signUp()
  const p = await api.projectWithRepos()
  const { ids } = await api.mappings(p.id, ['sales.order_detail'])
  const url = `/api/v1/projects/${p.id}/mappings/${ids[0]}`
  const view = await api.get<{ version: number; spec: { key: { template: string } } & Record<string, unknown> }>(url)
  await api.sendWithHeaders('PUT', url, { spec: { ...view.spec, key: { template: 'order::{order_id}' } } }, { 'If-Match': `"${view.version}"` })

  await new SignIn(page).signIn(api.email)
  await page.goto(`/projects/${p.id}/mappings/${ids[0]}`)
  await page.getByRole('tab', { name: /^Issues/ }).click()
  await page.getByRole('button', { name: 'Combine rows with the same key' }).click()
  const dialog = page.getByRole('dialog', { name: 'Combine rows with the same key' })
  await dialog.getByRole('textbox', { name: 'New array name' }).fill('products')
  await expect(dialog.getByRole('checkbox', { name: 'lineNo' })).toBeChecked()
  await dialog.getByRole('button', { name: 'Combine' }).click()

  await expect(page.getByRole('tab', { name: 'Composer' })).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByRole('button', { name: 'One document per: distinct order_id' })).toBeVisible()
  const sample = page.getByRole('region', { name: 'Sample' })
  const doc = sample.locator('pre').first()
  await expect(doc).toContainText('"products"', { timeout: 30_000 })
  // Order 1 has 2 lines: each once, in order.
  await expect(doc).toContainText('"lineNo": 1')
  await expect(doc).toContainText('"lineNo": 2')
  const text = (await doc.textContent()) ?? ''
  expect(text.match(/"lineNo"/g)?.length).toBe(2)
  await api.dispose()
})
