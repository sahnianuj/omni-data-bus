import { expect, test, type Locator, type Page } from '@playwright/test'
import { Api } from './support/api'
import { SignIn } from './support/pages'

// OC-048 AC1 (E2E): build an order document from blank in the composer against the real
// backend — items[] bound to sales.order_items by drag, items[].product to sales.products — and
// the sample shows each order's items with their product (OC-049 preview).

const tree = (page: Page) => page.getByRole('tree', { name: 'Document' })
const node = (page: Page, name: RegExp) => tree(page).getByRole('treeitem', { name })

async function rename(page: Page, name: string) {
  const input = page.getByRole('textbox', { name: /^Rename / })
  await input.fill(name)
  await input.press('Enter')
}

async function menu(page: Page, nodeName: string, action: string) {
  await page.getByRole('button', { name: `Actions for ${nodeName}` }).click()
  await page.getByRole('menuitem', { name: action }).click()
}

/** Drags a table from the Sources pane onto a node and binds it with the pre-filled option. */
async function bindByDrag(page: Page, table: string, target: Locator, option: RegExp) {
  await page.getByRole('list', { name: 'Sources' }).getByRole('button', { name: new RegExp(`^(Bind|Look up) ${table} `) }).first().dragTo(target)
  const dialog = page.getByRole('dialog', { name: new RegExp(`to ${table}$`) })
  await expect(dialog.getByRole('radio', { name: option })).toBeChecked()
  await dialog.getByRole('button', { name: 'Bind' }).click()
  await expect(dialog).toBeHidden()
}

test('AC1: composer builds an order with items and products', async ({ page }) => {
  const api = await Api.signUp()
  const p = await api.projectWithRepos()
  const { ids } = await api.mappings(p.id, ['sales.orders'])
  await new SignIn(page).signIn(api.email)
  await page.goto(`/projects/${p.id}/mappings/${ids[0]}`)

  await page.getByRole('tab', { name: 'Composer' }).click()
  await page.getByRole('button', { name: 'Start over' }).click()
  await page.getByRole('menuitem', { name: 'Blank' }).click()
  await page.getByRole('button', { name: 'Replace' }).click()

  // orderId at the root, then items[] with a product object.
  await node(page, /^\$/).click()
  await page.keyboard.press('Tab')
  await rename(page, 'orderId')
  await page.getByRole('combobox', { name: 'Type' }).selectOption('integer')
  await menu(page, '$', 'Add array')
  await rename(page, 'items')

  // Bind by drag: generated item fields are auto-mapped from order_items.
  await bindByDrag(page, 'order_items', node(page, /^items, array/), /^One-to-many: order_id = order_id/)
  await expect(node(page, /^items, array, bound to order_items/)).toBeVisible()
  await expect(node(page, /^quantity, integer, from order_items\.quantity/)).toBeVisible()

  await menu(page, 'items', 'Add object')
  await rename(page, 'product')
  await bindByDrag(page, 'products', node(page, /^product, object/), /^Many-to-one: product_id = product_id/)
  await expect(node(page, /^sku, string, from products\.sku/)).toBeVisible()

  // Map orderId from the driving table.
  await node(page, /^orderId,/).click()
  await page.getByRole('combobox', { name: 'Source for orderId' }).selectOption('|order_id|')
  await expect(page.getByText('Saved · just now')).toBeVisible()

  // The sample: real orders with their items, each holding its product; per-node counts.
  const sample = page.getByRole('region', { name: 'Sample' })
  await expect(sample.getByText(/"items"/).first()).toBeVisible({ timeout: 30_000 })
  await expect(sample.getByText(/"product"/).first()).toBeVisible()
  await expect(sample.getByText(/"sku"/).first()).toBeVisible()
  await expect(sample.getByRole('list', { name: 'Rows per node' })).toContainText('items.product')

  // The saved mapping is composed, driven by orders, with both bindings.
  const saved = await api.get<{ spec: { mode: string; root: { table: { name: string } }; relations: { id: string; table: { name: string }; parentId: string | null }[] } }>(
    `/api/v1/projects/${p.id}/mappings/${ids[0]}`,
  )
  expect(saved.spec.mode).toBe('composed')
  expect(saved.spec.root.table.name).toBe('orders')
  expect(saved.spec.relations.map((r) => r.table.name).sort()).toEqual(['order_items', 'products'])
  const items = saved.spec.relations.find((r) => r.table.name === 'order_items')!
  expect(items.parentId).toBeNull()
  expect(saved.spec.relations.find((r) => r.table.name === 'products')!.parentId).toBe(items.id)

  await api.dispose()
})
