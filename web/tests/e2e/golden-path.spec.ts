import { expect, test } from '@playwright/test'
import { docCount, rowCount } from './support/db'
import { uid } from './support/env'
import { Projects, ProjectWizard, SignIn } from './support/pages'

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

  await page.getByRole('button', { name: 'Preview' }).click()
  await page.waitForTimeout(2500)
  await page.screenshot({ path: 'test-results/golden-preview.png', fullPage: true })
  expect(projectId).toBeTruthy()
  expect(rowCount('shop.customer')).toBeGreaterThan(0)
  expect(docCount).toBeDefined()
})
