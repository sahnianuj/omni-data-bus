import { expect, test, type Response } from '@playwright/test'
import { Api } from './support/api'
import { couchbaseUser, docCount, dropCouchbaseUser, dropScope, psql, rowCount } from './support/db'
import { uid } from './support/env'
import { Monitor, Review, SignIn } from './support/pages'

test('AC6: reuse stored project credentials; secrets never returned', async ({ page }) => {
  test.setTimeout(300_000)
  // Credentials with passwords that appear nowhere else, so a leak can't hide behind a common word.
  const id = uid()
  const source = { username: `e2e_src_${id}`, password: `Src-${id}-${crypto.randomUUID()}` }
  const target = { username: `e2e_tgt_${id}`, password: `Tgt-${id}-${crypto.randomUUID()}` }
  psql(`CREATE ROLE ${source.username} LOGIN PASSWORD '${source.password}';
        GRANT USAGE ON SCHEMA shop TO ${source.username};
        GRANT SELECT ON ALL TABLES IN SCHEMA shop TO ${source.username}`)
  await couchbaseUser(target.username, target.password)
  try {
    const api = await Api.signUp()
    const p = await api.projectWithRepos(`Creds ${id}`, { source, target })
    const { scope } = await api.mappings(p.id, ['shop.customer'])
    await api.dispose()

    // Every response the browser receives from here on is checked for either password.
    const leaks: string[] = []
    const checks: Promise<void>[] = []
    page.on('response', (res: Response) => {
      checks.push(
        res
          .text()
          .then((body) => {
            if (body.includes(source.password) || body.includes(target.password)) leaks.push(`${res.request().method()} ${res.url()}`)
          })
          .catch(() => undefined), // redirects and aborted requests have no body
      )
    })

    // Sign in, sign out, sign in again: a fresh session, no password prompts.
    const signIn = new SignIn(page)
    await signIn.signIn(api.email)
    await page.getByRole('button', { name: 'Sign out' }).click()
    await expect(page).toHaveURL(/signin/)
    await signIn.signIn(api.email)

    // The saved source still works: test it without typing its password.
    await page.goto(`/projects/${p.id}/source`)
    await expect(page.getByText(source.username)).toBeVisible()
    await expect(page.getByText('Stored, encrypted')).toBeVisible()
    await expect(page.getByLabel('Password', { exact: true })).toHaveCount(0) // no prompt
    await page.getByRole('button', { name: 'Test connection' }).click()
    await expect(page.getByText(/^Passed/).first()).toBeVisible({ timeout: 60_000 })

    // Browse the tables and migrate, all on the stored credentials.
    await page.goto(`/projects/${p.id}/tables`)
    await expect(page.getByRole('checkbox', { name: /Select (shop\.)?customer$/ })).toBeVisible({ timeout: 30_000 })
    await new Review(page).migrate(p.id)
    const monitor = new Monitor(page)
    await monitor.waitFinished()
    await expect(page.getByRole('main').getByText('Completed', { exact: true }).first()).toBeVisible()
    expect(await docCount(scope, 'customer')).toBe(rowCount('shop.customer'))

    await Promise.all(checks)
    expect(checks.length).toBeGreaterThan(20)
    expect(leaks, 'responses containing a stored password').toEqual([])
    await dropScope(scope)
  } finally {
    psql(`DROP OWNED BY ${source.username}; DROP ROLE IF EXISTS ${source.username}`)
    await dropCouchbaseUser(target.username)
  }
})
