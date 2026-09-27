import { expect, test } from '@playwright/test'
import { Api } from './support/api'
import { docCount, dropScope, n1ql, psql } from './support/db'
import { uid } from './support/env'
import { Monitor, Review, SignIn } from './support/pages'

test('AC5: completed_with_errors → fix → retry failed rows', async ({ page }) => {
  test.setTimeout(300_000)
  // Dirty data: every 100th qty isn't a number.
  const table = `e2e_dirty_${uid()}`
  psql(`CREATE TABLE shop.${table} (id int PRIMARY KEY, qty text NOT NULL);
        INSERT INTO shop.${table} SELECT g, CASE WHEN g % 100 = 0 THEN 'n/a' ELSE g::text END FROM generate_series(1, 1000) g`)
  try {
    const api = await Api.signUp()
    const p = await api.projectWithRepos()
    const { scope, ids } = await api.mappings(p.id, [`shop.${table}`])
    // qty becomes an integer field; a strict parse turns each bad value into a row error, not a null.
    await api.editMapping(p.id, ids[0]!, (spec) => {
      const root = spec.docSchema as { root: { children: { name: string; type: string }[] } }
      root.root.children.find((f) => f.name === 'qty')!.type = 'integer'
    })
    await api.setTransforms(p.id, ids[0]!, 'qty', [{ op: 'parseInteger', args: { strict: true } }])

    await new SignIn(page).signIn(api.email)
    const first = await new Review(page).migrate(p.id)
    const monitor = new Monitor(page)
    await monitor.waitFinished()
    await expect(page.getByRole('main').getByText('Completed with errors').first()).toBeVisible()
    await expect(page.getByRole('region', { name: 'Failed' }).getByText('10', { exact: true })).toBeVisible()
    expect(await docCount(scope, table)).toBe(990)

    // The errors tab groups them by code and lists the failed rows.
    await page.getByRole('tab', { name: /Errors/ }).click()
    const errors = page.getByRole('tabpanel', { name: /Errors/ })
    await expect(errors.getByText('PARSE_FAILED').first()).toBeVisible()
    await expect(errors.getByText(/can't parse "n\/a"/).first()).toBeVisible()

    // Fix the transform: "n/a" means no quantity.
    await api.setTransforms(p.id, ids[0]!, 'qty', [
      { op: 'nullIf', args: { value: 'n/a' } },
      { op: 'parseInteger', args: { strict: true } },
    ])

    await errors.getByRole('button', { name: 'Retry failed rows' }).click()
    await page.getByRole('alertdialog', { name: 'Retry the failed rows?' }).or(page.getByRole('dialog', { name: 'Retry the failed rows?' }))
      .getByRole('button', { name: 'Retry failed rows' }).click()
    await expect(page).not.toHaveURL(new RegExp(`/jobs/${first}$`))
    await monitor.waitFinished()
    await expect(page.getByRole('main').getByText('Completed', { exact: true }).first()).toBeVisible()
    await expect(page.getByRole('region', { name: 'Failed' }).getByText('0', { exact: true })).toBeVisible()

    expect(await docCount(scope, table)).toBe(1000)
    const qty = async (id: number) =>
      (await n1ql<{ qty: number | null }>(`SELECT c.qty FROM \`sandbox\`.\`${scope}\`.\`${table}\` AS c WHERE c.id = ${id}`))[0]
    expect(await qty(100)).toEqual({ qty: null }) // the retried row, with the fixed transform
    expect(await qty(99)).toEqual({ qty: 99 })

    await dropScope(scope)
    await api.dispose()
  } finally {
    psql(`DROP TABLE IF EXISTS shop.${table}`)
  }
})
