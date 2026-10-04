import { readdirSync } from 'node:fs'
import { join, sep } from 'node:path'
import { expect, test } from '@playwright/test'
import { rowCount } from './support/db'
import { uid } from './support/env'
import { Monitor, Projects, ProjectWizard, Review, SignIn } from './support/pages'

// The all-in-one image (OC-072 AC6, OC-074 AC1): only against it, e.g.
//   E2E_APPLIANCE=1 E2E_BASE_URL=http://localhost:18080 E2E_SOURCE_HOST=host.docker.internal E2E_SOURCE_PORT=5434 \
//     npx playwright test appliance
test.skip(!process.env.E2E_APPLIANCE, 'runs against the all-in-one image only (E2E_APPLIANCE=1)')

test('OC-072 AC6: migrate into the bundled Couchbase', async ({ page }) => {
  await new SignIn(page).signUp()
  const projectId = await new Projects(page).create(`Appliance ${uid()}`)
  const wizard = new ProjectWizard(page)
  await wizard.sourceStep()
  await page.getByRole('link', { name: 'Next: target repository' }).or(page.getByRole('button', { name: 'Next: target repository' })).click()

  // No address or password to type: one click saves and tests the container's own Couchbase.
  await page.getByRole('button', { name: 'Use the bundled Couchbase' }).click()
  await expect(page.getByText('couchbase://127.0.0.1')).toBeVisible({ timeout: 60_000 })
  await expect(page.getByText(/^Passed/).first()).toBeVisible()
  await expect(page.getByRole('contentinfo', { name: 'Build information' })).toContainText(/Couchbase Server CE [\d.]+ \(bundled\)/)

  await page.getByRole('link', { name: 'Next: select tables' }).or(page.getByRole('button', { name: 'Next: select tables' })).click()
  await page.getByRole('button', { name: 'Explore schema' }).or(page.getByRole('link', { name: 'Explore schema' })).click()
  await expect(page.getByText(/Last discovered/)).toBeVisible({ timeout: 60_000 })
  await page.getByRole('navigation', { name: 'Project steps' }).getByRole('link', { name: /Tables/ }).click()
  await page.getByRole('combobox').filter({ hasText: 'Select a bucket' }).click()
  await page.getByRole('option', { name: /sandbox/ }).click()
  await page.getByRole('button', { name: 'New scope' }).click()
  const scope = `e2e_${uid()}`
  await page.getByRole('dialog').getByRole('textbox', { name: 'Name' }).fill(scope)
  await page.getByRole('dialog').getByRole('button', { name: 'Create' }).click()
  await expect(page.getByRole('combobox').filter({ hasText: scope })).toBeVisible()
  await page.getByRole('checkbox', { name: /Select (shop\.)?customer$/ }).check()
  await page.getByRole('button', { name: /Create 1 mapping/ }).click()
  await expect(page.getByText('Created 1 mapping')).toBeVisible()

  await new Review(page).migrate(projectId)
  const monitor = new Monitor(page)
  await monitor.waitFinished()
  await expect(monitor.progress()).toHaveAttribute('aria-valuenow', '100')
  const rows = rowCount('shop.customer')
  await expect(monitor.summary().getByRole('heading', { name: `Migrated ${rows.toLocaleString('en-US')} documents` })).toBeVisible()
  await expect(monitor.summary().getByText('Passed', { exact: true })).toBeVisible({ timeout: 60_000 })
})

// OC-080 AC1: a directory target writes into the folder shared from the computer, e.g. with
//   docker run … -v "$E2E_OUTPUT_DIR:/data" -e ODB_OUTPUT_HOST_DIR="$E2E_OUTPUT_DIR" …
test('OC-080 AC1: directory target writes to the computer', async ({ page }) => {
  const out = process.env.E2E_OUTPUT_DIR
  test.skip(!out, 'needs E2E_OUTPUT_DIR: the folder shared as /data')
  await new SignIn(page).signUp()
  const projectId = await new Projects(page).create(`Appliance dir ${uid()}`)
  await new ProjectWizard(page).sourceStep()
  await page.getByRole('link', { name: 'Next: target repository' }).or(page.getByRole('button', { name: 'Next: target repository' })).click()

  await page.getByRole('radio', { name: /Directory of JSON files/ }).click()
  await expect(page.getByText(`Choose a folder in /data: that's ${out} on your computer.`)).toBeVisible()
  await page.getByRole('button', { name: 'Choose folder…' }).click()
  const dialog = page.getByRole('dialog', { name: 'Choose the output folder' })
  await dialog.getByRole('button', { name: `Your computer: ${out}` }).click()
  const folder = `e2e_${uid()}`
  await dialog.getByRole('button', { name: 'New folder' }).click()
  await dialog.getByLabel('Folder name').fill(folder)
  await dialog.getByRole('button', { name: 'Create' }).click()
  await expect(dialog.getByText(`on your computer: ${out}/${folder}`)).toBeVisible()
  await dialog.getByRole('button', { name: 'Use this folder' }).click()
  await page.getByRole('button', { name: 'Test connection' }).click()
  await expect(page.getByText(/^Passed/).first()).toBeVisible({ timeout: 60_000 })
  await page.getByRole('button', { name: 'Save to project' }).click()
  await expect(page.getByText(`on your computer: ${out}/${folder}`).first()).toBeVisible()

  await page.getByRole('link', { name: 'Next: select tables' }).or(page.getByRole('button', { name: 'Next: select tables' })).click()
  await page.getByRole('button', { name: 'Explore schema' }).or(page.getByRole('link', { name: 'Explore schema' })).click()
  await expect(page.getByText(/Last discovered/)).toBeVisible({ timeout: 60_000 })
  await page.getByRole('navigation', { name: 'Project steps' }).getByRole('link', { name: /Tables/ }).click()
  await page.getByRole('checkbox', { name: /Select (shop\.)?customer$/ }).check()
  await page.getByRole('button', { name: /Create 1 mapping/ }).click()
  await expect(page.getByText('Created 1 mapping')).toBeVisible()

  await new Review(page).migrate(projectId)
  const monitor = new Monitor(page)
  await monitor.waitFinished()
  const rows = rowCount('shop.customer')
  await expect(monitor.summary().getByRole('heading', { name: `Migrated ${rows.toLocaleString('en-US')} documents` })).toBeVisible()

  // One JSON file per row, on the computer (not counting the run's manifest).
  const files = readdirSync(join(out!, folder), { recursive: true, encoding: 'utf8' }).filter((f) => f.endsWith('.json') && !f.split(sep).some((p) => p.startsWith('.') || p.startsWith('_')))
  expect(files.length).toBe(rows)
})
