import { execFileSync } from 'node:child_process'
import { mkdtempSync, readdirSync, readFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { expect, test } from '@playwright/test'
import { Api } from './support/api'
import { rowCount } from './support/db'
import { uid } from './support/env'
import { SignIn } from './support/pages'

/** OC-056 AC1, OC-182, OC-184 AC1: export the migration folder from the UI (config.json,
 * mappings.json, README.md — no script), run it with the odb commands alone (the README's
 * docker command, on the dev stack's network), then import its report.json — the run shows as
 * External. */
test('OC-184 AC1: export, run it with odb alone, import the report', async ({ page }) => {
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

  // Review → Export… → New export: the project read-only, then the options; no passphrase.
  await page.goto(`/projects/${p.id}/review`)
  await page.getByRole('link', { name: 'Export…' }).click()
  await expect(page.getByRole('link', { name: 'Guide: running migrations outside the tool' })).toBeVisible()
  await page.getByRole('button', { name: 'New export' }).click()
  const dialog = page.getByRole('dialog', { name: 'Export migration' })
  await expect(dialog.getByRole('region', { name: /Mappings: 1 of 1 ready/ })).toBeVisible()
  await dialog.getByRole('button', { name: 'Next' }).click()
  const downloaded = page.waitForEvent('download')
  await dialog.getByRole('button', { name: 'Export' }).click()
  const zip = await downloaded
  await expect(page.getByRole('dialog', { name: 'Export downloaded' }).getByLabel('Files in the export')).toContainText('mappings.json')

  const dir = mkdtempSync(join(tmpdir(), 'odb-export-'))
  await zip.saveAs(join(dir, 'export.zip'))
  execFileSync('unzip', ['-q', 'export.zip'], { cwd: dir })
  const folder = join(dir, readdirSync(dir).find((f) => f.startsWith('odb-export-'))!)
  // OC-182 AC1: nothing OS-specific — no script, no binary.
  expect(readdirSync(folder).sort()).toEqual(['README.md', 'config.json', 'mappings.json'])
  const readme = readFileSync(join(folder, 'README.md'), 'utf8')
  for (const cmd of ['odb preflight', 'odb run', 'odb status']) expect(readme).toContain(cmd)
  expect(readme).not.toContain('migrate.sh')

  // The README's docker command (OC-183), on the dev stack's network: odb finds config.json and
  // mappings.json in the working directory; the JSON files go inside the folder.
  const out = join(folder, 'out')
  const odb = (cmd: string) =>
    execFileSync('docker', [
      'run', '--rm', '--user', `${process.getuid?.() ?? 0}:${process.getgid?.() ?? 0}`, '-v', `${folder}:${folder}`, '-w', folder,
      '--network', 'omni-connect-dev_default', process.env.ODB_IMAGE ?? 'omni-data-bus:e2e', cmd, '--target-path', out,
    ], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }) // progress and logs stay out of the test output
  expect(odb('preflight')).toContain('ready to run')
  odb('run')
  expect(odb('verify')).toContain('OK ')
  expect(readdirSync(join(out, 'orders')).filter((f) => f.endsWith('.json'))).toHaveLength(rowCount('shop.orders'))

  // Import report.json: the project gets an External run with the counts.
  await page.goto(`/projects/${p.id}/export`)
  await page.getByLabel('Import report').setInputFiles(join(folder, 'odb-state', 'report.json'))
  await expect(page).toHaveURL(/\/jobs\/[^/]+$/)
  await expect(page.getByRole('region', { name: 'External run' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Written' })).toContainText(rowCount('shop.orders').toLocaleString('en'))
  await api.dispose()
})
