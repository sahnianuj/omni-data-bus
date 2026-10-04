import { expect, test } from '@playwright/test'
import { Api } from './support/api'
import { docCount, dropScope, getDoc, psql, rowCount } from './support/db'
import { Monitor, Review, SignIn } from './support/pages'

type Order = Record<string, unknown>

/** OC-056 AC1: on the canvas, embed the order_items → products path and the tags many-to-many,
 * preview, migrate to Couchbase and check a document. */
test('AC1: nested and many-to-many relations', async ({ page }) => {
  const api = await Api.signUp()
  const p = await api.projectWithRepos()
  const { scope, ids } = await api.mappings(p.id, ['sales.orders'])
  await new SignIn(page).signIn(api.email)
  await page.goto(`/projects/${p.id}/mappings/${ids[0]}`)
  const related = page.getByRole('region', { name: 'Related tables' })

  // order_items, then products inside each item: one two-hop embed.
  await related.getByRole('button', { name: 'Show tables related to order_items' }).click()
  await related.getByTestId('related-order_items/products').getByRole('button', { name: 'Embed' }).click()
  const hops = page.getByRole('dialog').filter({ has: page.getByRole('button', { name: 'Embed' }) })
  await expect(hops.getByRole('link', { name: 'Guide: relationship mapping' })).toBeVisible()
  await hops.getByRole('button', { name: 'Embed', exact: true }).click()
  await expect(hops).toBeHidden()

  // tags through order_tags.
  await related.getByRole('button', { name: 'Embed as array of tags' }).click()
  const m2m = page.getByRole('dialog').filter({ has: page.getByRole('button', { name: 'Embed' }) })
  await m2m.getByRole('button', { name: 'Embed', exact: true }).click()
  await expect(m2m).toBeHidden()
  await expect(page.getByText('All changes saved')).toBeVisible()

  await page.getByRole('button', { name: 'Preview' }).click()
  const preview = page.getByRole('dialog', { name: 'Preview' }).or(page.getByRole('region', { name: 'Preview' }))
  await expect(preview.getByText(/"tags": \[/).first()).toBeVisible()

  await new Review(page).migrate(p.id)
  const monitor = new Monitor(page)
  await monitor.waitFinished()
  await expect(monitor.summary().getByText('Passed', { exact: true })).toBeVisible({ timeout: 60_000 })
  expect(await docCount(scope, 'orders')).toBe(rowCount('sales.orders'))

  // The order with the most items carries all of them, each with its product, and its tags.
  const [orderId, items] = psql('SELECT order_id, count(*) FROM sales.order_items GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT 1').split('|').map(Number)
  const tags = Number(psql(`SELECT count(*) FROM sales.order_tags WHERE order_id = ${orderId}`))
  const doc = await getDoc<Order>(scope, 'orders', `orders::${orderId}`)
  const arrays = Object.values(doc ?? {}).filter(Array.isArray) as Record<string, unknown>[][]
  const itemArray = arrays.find((a) => a.length > 0 && Object.values(a[0]!).some((v) => v && typeof v === 'object' && 'sku' in (v as object)))
  expect(itemArray, JSON.stringify(doc)).toHaveLength(items!)
  expect(doc!.tags).toHaveLength(tags)
  await dropScope(scope)
  await api.dispose()
})
