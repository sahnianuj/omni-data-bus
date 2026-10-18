import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { expect, test, type Page } from '@playwright/test'
import { Api } from './support/api'
import { SignIn } from './support/pages'

// OC-173 (E2E): design-first mapping against the real backend and the dev stack's pos fixture
// (OC-162): import the cart design with its payment designs, get the whole mapping proposed
// from the connected source, fine-tune it in review, apply, preview, migrate; then a second
// "New mapping from a design" of the same table asks before updating it (OC-170).

const designs = resolve(process.cwd(), '../backend/internal/fixtures/pos/designs')
const file = (rel: string) => ({ name: rel.split('/').at(-1)!, mimeType: 'application/json', buffer: readFileSync(resolve(designs, rel)) })
const tree = (page: Page) => page.getByRole('tree', { name: 'Document' })
const node = (page: Page, name: RegExp) => tree(page).getByRole('treeitem', { name }).first()

test('import cart, review, apply, preview, migrate', async ({ page }) => {
  test.setTimeout(240_000)
  const api = await Api.signUp()
  const p = await api.projectWithRepos()
  const { ids } = await api.mappings(p.id, ['pos.carts'])
  await new SignIn(page).signIn(api.email)
  await page.goto(`/projects/${p.id}/mappings/${ids[0]}`)
  await page.getByRole('tab', { name: 'Composer' }).click()

  // The cart design and its five payment designs, imported together, proposing the mapping.
  await page.getByRole('button', { name: 'Import JSON design…' }).click()
  const dialog = page.getByRole('dialog', { name: 'Import a JSON design' })
  await dialog.getByLabel('JSON or NDJSON file').setInputFiles([
    file('cart.schema.json'),
    ...['scc', 'svc', 'credit_card', 'cash', 'scan_and_pay'].map((n) => file(`payments/${n}.schema.json`)),
  ])
  await expect(dialog.getByTestId('import-extra')).toContainText('with 5 more files')
  await dialog.getByRole('button', { name: 'Read design' }).click()
  await dialog.getByRole('checkbox', { name: /Propose the mapping from the source/ }).check()
  await dialog.getByRole('button', { name: 'Replace design' }).click()

  // Review: the proposal on the customer's tree, from pos.carts.
  const banner = page.getByRole('region', { name: 'Proposed mapping' })
  await expect(banner).toContainText('from pos.carts', { timeout: 30_000 })
  await expect(node(page, /^customer, object, bound to customers, high confidence/)).toBeVisible()
  await expect(node(page, /^payments, array, bound to collected_payments/)).toBeVisible()
  await expect(node(page, /^SCC, variant/)).toBeVisible()
  await expect(node(page, /^price, integer, from cart_items\.price/)).toContainText('× 100')

  // Fine-tune: take back partner.name's combined name, mark updatedAt not populated.
  await banner.getByRole('radio', { name: /Needs review/ }).click()
  await node(page, /^name, string, from ƒ|^name, string, .*medium confidence/).click()
  const panel = page.getByRole('region', { name: 'Proposal' })
  await panel.getByRole('button', { name: 'Take back' }).click()
  await banner.getByRole('radio', { name: /Unresolved/ }).click()
  await node(page, /^updatedAt,/).click()
  await panel.getByRole('button', { name: 'Mark not populated' }).click()
  await banner.getByRole('radio', { name: /Edited/ }).click()
  await expect(tree(page).getByRole('treeitem', { name: /edited/ })).toHaveCount(2)
  await banner.getByRole('button', { name: 'Apply', exact: true }).click()
  await expect(banner).toBeHidden()
  await expect(page.getByText('Saved · just now')).toBeVisible({ timeout: 15_000 })

  // The sample: carts in the customer's shape, payments in their variant's.
  const sample = page.getByRole('region', { name: 'Sample' })
  await expect(sample.getByText(/"paymentType"/).first()).toBeVisible({ timeout: 30_000 })

  // Migrate: every cart becomes a document.
  const job = await api.startJob(p.id)
  const done = await api.waitJob(job.id, ['completed', 'completed_with_errors'], 180_000)
  expect(done.status).toBe('completed')
  await api.waitWritten(job.id, 200)

  // OC-170: a new mapping from the same design asks first; Update opens this mapping's import.
  await page.goto(`/projects/${p.id}/mappings`)
  await page.getByRole('button', { name: 'New mapping from a design…' }).click()
  const fresh = page.getByRole('dialog', { name: 'New mapping from a design' })
  await fresh.getByLabel('JSON or NDJSON file').setInputFiles(file('cart.schema.json'))
  await fresh.getByRole('button', { name: 'Find the table' }).click()
  await expect(fresh.getByRole('radio', { name: 'pos.carts' })).toBeChecked()
  await fresh.getByRole('button', { name: 'Create and propose' }).click()
  const asked = page.getByRole('dialog', { name: 'pos.carts is already mapped' })
  await expect(asked.getByRole('button', { name: 'Create a new mapping' })).toBeDisabled()
  await asked.getByRole('button', { name: 'Update this mapping' }).click()
  await expect(page.getByRole('dialog', { name: 'Import a JSON design' })).toBeVisible({ timeout: 15_000 })
})
