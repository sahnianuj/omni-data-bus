import { expect, test } from '@playwright/test'
import { docCount, dropScope, rowCount } from './support/db'
import { uid } from './support/env'
import { Monitor, Projects, ProjectWizard, Review, SignIn } from './support/pages'

test('AC1: project → repos → map → migrate → verify', async ({ page }) => {
  await new SignIn(page).signUp()
  const projectId = await new Projects(page).create(`Golden ${uid()}`)
  const wizard = new ProjectWizard(page)
  await wizard.sourceStep()
  await page.getByRole('link', { name: 'Next: target repository' }).or(page.getByRole('button', { name: 'Next: target repository' })).click()
  await wizard.targetStep()
  await expect(page.getByText('Too many connection tests')).toHaveCount(0)
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
  await page.getByRole('button', { name: 'Open' }).or(page.getByRole('link', { name: 'Open' })).first().click()
  const header = page.getByText(/auto-mapped \d+ of \d+ columns/).first()
  await expect(header).toBeVisible()
  const [, auto, total] = (await header.textContent())!.match(/auto-mapped (\d+) of (\d+)/)!
  expect(Number(auto)).toBeGreaterThan(0)
  expect(Number(auto)).toBeLessThanOrEqual(Number(total))

  // Nothing is left to map by hand here (every column auto-maps); preview a few documents.
  await page.getByRole('button', { name: 'Preview' }).click()
  await expect(page.getByText(/✓ \d+ valid/).first()).toBeVisible()

  const jobId = await new Review(page).migrate(projectId)
  const monitor = new Monitor(page)
  await monitor.waitFinished()
  await expect(monitor.progress()).toHaveAttribute('aria-valuenow', '100')
  await expect(monitor.summary().getByRole('heading', { name: /^Migrated [\d,]+ documents/ })).toBeVisible()
  // Verification (OC-030) runs after the last write.
  await expect(monitor.summary().getByText('Passed', { exact: true })).toBeVisible({ timeout: 60_000 })

  const rows = rowCount('shop.customer')
  expect(rows).toBeGreaterThan(0)
  expect(await docCount(scope, 'customer')).toBe(rows)
  expect(jobId).toBeTruthy()
  await dropScope(scope)
})
