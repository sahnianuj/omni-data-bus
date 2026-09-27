import { expect, test } from '@playwright/test'
import { Api } from './support/api'
import { docCount, dropScope, getDoc, psql, rowCount } from './support/db'
import { Monitor, Review, SignIn } from './support/pages'

test('AC3: orders embedded as array', async ({ page }) => {
  const api = await Api.signUp()
  const p = await api.projectWithRepos()
  const { scope, ids } = await api.mappings(p.id, ['shop.customer'])
  await new SignIn(page).signIn(api.email)
  await page.goto(`/projects/${p.id}/mappings/${ids[0]}`)

  // Related tables → embed orders (one-to-many) as an array with generated, auto-mapped fields.
  const related = page.getByRole('region', { name: 'Related tables' })
  await related.getByRole('listitem').filter({ hasText: /orders via customer_id/ }).getByRole('button', { name: 'Embed' }).click()
  const dialog = page.getByRole('dialog', { name: 'Embed orders' })
  await expect(dialog.getByText('Array of objects (one-to-many)')).toBeVisible()
  await dialog.getByRole('combobox', { name: 'Order items by' }).selectOption('order_id')
  await dialog.getByRole('button', { name: 'Embed' }).click()
  await expect(dialog).toBeHidden()
  await expect(page.getByText('orders → orders[] (array)')).toBeVisible()
  await expect(related.getByRole('listitem').filter({ hasText: /orders via customer_id/ }).getByText('Embedded')).toBeVisible()
  await expect(page.getByText(/\d+ of \d+ embedded columns mapped/)).toBeVisible()
  await expect(page.getByText('All changes saved')).toBeVisible()

  // The preview builds documents with the array.
  await page.getByRole('button', { name: 'Preview' }).click()
  const preview = page.getByRole('dialog', { name: 'Preview' }).or(page.getByRole('region', { name: 'Preview' }))
  await expect(preview.getByText(/"orders": \[/).first()).toBeVisible()

  const jobId = await new Review(page).migrate(p.id)
  const monitor = new Monitor(page)
  await monitor.waitFinished()
  await expect(monitor.summary().getByText('Passed', { exact: true })).toBeVisible({ timeout: 60_000 })
  expect(jobId).toBeTruthy()

  // Spot-check: the customer with the most orders carries all of them, in order_id order.
  const [cid, n] = psql('SELECT customer_id, count(*) FROM shop.orders GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT 1').split('|').map(Number)
  const doc = await getDoc<{ orders: { orderId: number }[] }>(scope, 'customer', `customer::${cid}`)
  expect(doc?.orders).toHaveLength(n!)
  const ids2 = doc!.orders.map((o) => o.orderId)
  expect(ids2).toEqual([...ids2].sort((a, b) => a - b))
  expect(doc!.orders[0]).not.toHaveProperty('customerId') // the join column stays out of the items
  expect(await docCount(scope, 'customer')).toBe(rowCount('shop.customer'))

  await dropScope(scope)
  await api.dispose()
})
