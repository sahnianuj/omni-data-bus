import { expect, test } from '@playwright/test'
import { Api } from './support/api'
import { rowCount } from './support/db'
import { uid } from './support/env'
import { Monitor, Review, SignIn } from './support/pages'

/** OC-056 AC1: a project writing JSON files — directory target → map orders → migrate → find 1001 by key. */
test('AC1: directory target end to end', async ({ page }) => {
  const api = await Api.signUp()
  const p = await api.send<{ id: string }>('POST', '/api/v1/projects', { name: `Dir ${uid()}` })
  await api.sourceRepo(p.id)
  await new SignIn(page).signIn(api.email)

  // Target step: a folder of JSON files inside the shared volume.
  await page.goto(`/projects/${p.id}/target`)
  await page.getByRole('radio', { name: /Directory of JSON files/ }).click()
  await expect(page.getByRole('link', { name: 'Guide: directory targets' })).toHaveAttribute('href', /directory-targets\.md$/)
  const folder = `/data/odb-out/e2e_${uid()}`
  await page.getByLabel('Folder path').fill(folder)
  await page.getByText('Advanced · compression, file mode, size').click()
  await page.getByLabel("Create the folder if it's missing").check()
  await page.getByRole('button', { name: 'Test connection' }).click()
  await expect(page.getByText(/^Passed/).first()).toBeVisible({ timeout: 60_000 })
  await page.getByRole('button', { name: 'Save to project' }).click()
  await expect(page.getByText(folder).first()).toBeVisible()

  await api.discover(`${p.id}::source`)
  await api.send('POST', `/api/v1/projects/${p.id}/mappings:bulk`, {
    items: [{ sourceSchema: 'shop', sourceTable: 'orders', location: { kind: 'directory', folder: 'orders', createIfMissing: true } }],
  })

  const jobId = await new Review(page).migrate(p.id)
  const monitor = new Monitor(page)
  await monitor.waitFinished()
  await expect(monitor.summary().getByText('Passed', { exact: true })).toBeVisible({ timeout: 60_000 })
  const job = await api.job(jobId)
  expect(job.progress.written).toBe(rowCount('shop.orders'))

  // Output tab: the file of order 1001, found by its primary key.
  await page.getByRole('tab', { name: 'Output' }).click()
  await page.getByLabel('order_id').fill('1001')
  await page.getByRole('button', { name: 'Find' }).click()
  const doc = page.getByRole('article', { name: 'Document' })
  await expect(doc).toContainText('"orderId": 1001')
  await expect(doc).toContainText('orders/orders%3A%3A1001.json')
  await api.dispose()
})
