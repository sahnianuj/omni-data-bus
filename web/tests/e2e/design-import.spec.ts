import { expect, test } from '@playwright/test'
import { Api } from './support/api'
import { docCount, dropScope, getDoc, psql, rowCount } from './support/db'
import { Monitor, Review, SignIn } from './support/pages'

// The target team's document shape: some names differ from the columns, two types differ
// (phoneNumber is a number, active a string) and loyaltyTier has no source at all.
const sample = {
  customerId: 1,
  fname: 'Ada',
  lname: 'Lovelace',
  emailAddr: 'ada@example.com',
  active: 'yes',
  dob: '1815-12-10',
  phoneNumber: 4402079460000,
  loyaltyTier: 'gold',
}

test('AC2: drop JSON sample, review suggestions, migrate', async ({ page }) => {
  const api = await Api.signUp()
  const p = await api.projectWithRepos()
  const { scope, ids } = await api.mappings(p.id, ['shop.customer'])
  await new SignIn(page).signIn(api.email)
  await page.goto(`/projects/${p.id}/mappings/${ids[0]}`)
  const score = page.getByRole('region', { name: 'Mapping score' })
  // OC-178: the headline counts the document's fields; the auto-map run is the detail under it.
  await expect(score.getByRole('heading', { name: /target fields? done/ })).toBeVisible()
  await expect(score.getByText('Source columns · auto-mapped 15 of 15 on the last run')).toBeVisible()

  // Design → Import → JSON sample → Replace.
  await page.getByRole('tab', { name: 'Design' }).click()
  await page.getByRole('button', { name: 'Import' }).click()
  await page.getByRole('menuitem', { name: 'JSON sample' }).click()
  await page.getByRole('dialog', { name: 'JSON sample' }).getByRole('textbox', { name: 'JSON' }).fill(JSON.stringify(sample, null, 2))
  await page.getByRole('dialog', { name: 'JSON sample' }).getByRole('button', { name: 'Preview changes' }).click()
  const diff = page.getByRole('dialog', { name: 'Review design changes' })
  await expect(diff.getByText(/\+ loyaltyTier/)).toBeVisible()
  await diff.getByRole('button', { name: 'Replace' }).click()
  await expect(page.getByText('From JSON sample')).toBeVisible()

  // The score reflects a partial match, with a suggestion waiting for review.
  const headline = score.getByRole('heading', { name: /auto-mapped \d+ of 15 columns/ })
  await expect(headline).not.toHaveText(/15 of 15/)
  const auto = Number((await headline.textContent())!.match(/auto-mapped (\d+)/)![1])
  expect(auto).toBeGreaterThan(0)
  await expect(score.getByRole('button', { name: /^[1-9]\d* suggested$/ }).first()).toBeVisible()

  // Review and accept the suggestions.
  await page.getByRole('tab', { name: 'Canvas' }).click()
  const panel = page.getByRole('complementary', { name: 'Mapping panels' })
  await panel.getByRole('tab', { name: 'Suggestions' }).click()
  await panel.getByRole('button', { name: 'Accept all', exact: true }).click()
  await expect(page.getByText(/Accepted \d+ suggestions?/).first()).toBeVisible()
  await expect(score.getByRole('button', { name: '0 suggested' }).first()).toBeVisible()

  // loyaltyTier has no source column: make it optional so the mapping validates.
  await page.getByRole('tab', { name: 'Design' }).click()
  await page.getByRole('checkbox', { name: 'loyaltyTier is required' }).uncheck()
  await expect(page.getByText('All changes saved')).toBeVisible()
  await expect(score.getByRole('button', { name: /required fields? unmapped/ })).toHaveCount(0)

  // Edited mappings are validated again before they can migrate.
  await page.getByRole('button', { name: /^Validate/ }).click()
  await expect.poll(async () => (await api.mapping(p.id, ids[0]!)).status, { timeout: 15_000 }).toBe('ready')

  await new Review(page).migrate(p.id)
  const monitor = new Monitor(page)
  await monitor.waitFinished()
  await expect(page.getByRole('main').getByText('Completed', { exact: true }).first()).toBeVisible()
  expect(await docCount(scope, 'customer')).toBe(rowCount('shop.customer'))

  // Documents have the imported shape, filled from the source row.
  const [first, last] = psql('SELECT first_name, last_name FROM shop.customer WHERE customer_id = 1').split('|')
  const doc = await getDoc<Record<string, unknown>>(scope, 'customer', 'customer::1')
  expect(doc).toMatchObject({ customerId: 1, fname: first, lname: last })
  for (const field of ['emailAddr', 'active', 'dob', 'phoneNumber']) expect(doc).toHaveProperty(field)
  expect(doc).not.toHaveProperty('firstName')

  await dropScope(scope)
  await api.dispose()
})
