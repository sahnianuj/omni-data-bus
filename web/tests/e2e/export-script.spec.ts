import { execFileSync } from 'node:child_process'
import { mkdtempSync, readdirSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { expect, test } from '@playwright/test'
import { Api } from './support/api'
import { rowCount } from './support/db'
import { password, uid } from './support/env'
import { SignIn } from './support/pages'

/** OC-056 AC1: export a sealed migrate.sh from the UI, run it in a container against the fixture
 * database, then import its report.json — the run shows as External. */
test('AC1: export, run the script, import the report', async ({ page }) => {
  const api = await Api.signUp()
  const p = await api.send<{ id: string }>('POST', '/api/v1/projects', { name: `Export ${uid()}` })
  await api.sourceRepo(p.id)
  await api.send('PUT', `/api/v1/projects/${p.id}/connections/target`, {
    type: 'directory', config: { path: `/data/odb-out/e2e_${uid()}`, createIfMissing: true },
  })
  await api.send('POST', `/api/v1/connections/${encodeURIComponent(`${p.id}::target`)}/test`)
  await api.discover(`${p.id}::source`)
  await api.send('POST', `/api/v1/projects/${p.id}/mappings:bulk`, {
    items: [{ sourceSchema: 'shop', sourceTable: 'orders', location: { kind: 'directory', folder: 'orders', createIfMissing: true } }],
  })
  await new SignIn(page).signIn(api.email)

  // Review → Export… → New export: the project read-only, a generated passphrase, the script.
  await page.goto(`/projects/${p.id}/review`)
  await page.getByRole('link', { name: 'Export…' }).click()
  await expect(page.getByRole('link', { name: 'Guide: running migrations outside the tool' })).toBeVisible()
  await page.getByRole('button', { name: 'New export' }).click()
  const dialog = page.getByRole('dialog', { name: 'Export migration' })
  await expect(dialog.getByRole('region', { name: /Mappings: 1 of 1 ready/ })).toBeVisible()
  await dialog.getByRole('button', { name: 'Next' }).click()
  await dialog.getByRole('button', { name: 'Generate' }).click()
  await expect(dialog.getByLabel('Passphrase')).not.toHaveValue('')
  await dialog.getByRole('button', { name: 'Next' }).click()
  await dialog.getByRole('button', { name: 'Export' }).click()
  // The session confirms the password first.
  await dialog.getByLabel(/Confirm your password/).fill(password)
  const downloaded = page.waitForEvent('download')
  await dialog.getByRole('button', { name: 'Confirm and export' }).click()
  const zip = await downloaded
  const passphrase = (await page.getByRole('dialog', { name: 'Export downloaded' }).locator('code').innerText()).trim()
  expect(passphrase.split('-')).toHaveLength(6)

  const dir = mkdtempSync(join(tmpdir(), 'odb-export-'))
  await zip.saveAs(join(dir, 'export.zip'))
  execFileSync('unzip', ['-q', 'export.zip'], { cwd: dir })
  // The script runs odb from the e2e image on the dev stack's network.
  const run = (cmd: string) =>
    execFileSync(join(dir, 'migrate.sh'), [cmd], {
      cwd: dir,
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'pipe'], // progress and logs stay out of the test output
      env: {
        ...process.env, ODB_RUNTIME: 'docker', ODB_DOCKER_ARGS: '--network omni-connect-dev_default', ODB_BUNDLE_PASSPHRASE: passphrase,
        ODB_TARGET_PATH: join(dir, 'out'), ODB_STATE_DIR: join(dir, 'state'),
      },
    })
  run('run')
  expect(readdirSync(join(dir, 'out', 'orders')).filter((f) => f.endsWith('.json'))).toHaveLength(rowCount('shop.orders'))

  // Import report.json: the project gets an External run with the counts.
  await page.goto(`/projects/${p.id}/export`)
  await page.getByLabel('Import report').setInputFiles(join(dir, 'state', 'report.json'))
  await expect(page).toHaveURL(/\/jobs\/[^/]+$/)
  await expect(page.getByRole('region', { name: 'External run' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Written' })).toContainText(rowCount('shop.orders').toLocaleString('en'))
  await api.dispose()
})
