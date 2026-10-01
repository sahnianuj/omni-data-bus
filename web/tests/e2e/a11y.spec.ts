import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { Api } from './support/api'
import { dropScope } from './support/db'
import { SignIn } from './support/pages'

// WCAG 2.1 A and AA, on every main screen, in both themes.
const TAGS = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']

async function axe(page: Page, screen: string) {
  // Let data load and transitions settle before scanning.
  await page.waitForLoadState('networkidle')
  await page.waitForTimeout(500)
  const { violations } = await new AxeBuilder({ page }).withTags(TAGS).analyze()
  const report = violations.map((v) => `${v.id} (${v.impact}): ${v.help}\n    ${v.nodes.map((n) => n.target.join(' ')).slice(0, 5).join('\n    ')}`)
  expect(report, `${screen}: axe violations`).toEqual([])
}

for (const theme of ['Light', 'Dark'] as const) {
  test(`AC8: axe on all main screens (${theme.toLowerCase()})`, async ({ page }) => {
    test.setTimeout(240_000)
    const api = await Api.signUp()
    const p = await api.projectWithRepos()
    const { scope, ids } = await api.mappings(p.id, ['shop.customer'])
    const job = await api.startJob(p.id)
    await api.waitJob(job.id, ['completed'])

    await page.goto('/signin')
    await axe(page, 'sign in')
    await new SignIn(page).signIn(api.email)
    await page.getByRole('radiogroup', { name: 'Change theme' }).getByRole('radio', { name: theme }).check()
    await expect(page.locator('html')).toHaveClass(theme === 'Dark' ? /dark/ : /^(?!.*dark)/)

    const screens: [string, string][] = [
      ['dashboard', '/'],
      ['projects', '/projects'],
      ['source repository', `/projects/${p.id}/source`],
      ['schema explorer', `/projects/${p.id}/source/schema`],
      ['target repository', `/projects/${p.id}/target`],
      ['target keyspaces', `/projects/${p.id}/target/keyspaces`],
      ['tables', `/projects/${p.id}/tables`],
      ['mappings', `/projects/${p.id}/mappings`],
      ['mapping workbench', `/projects/${p.id}/mappings/${ids[0]}`],
      ['review', `/projects/${p.id}/review`],
      ['job monitor', `/projects/${p.id}/jobs/${job.id}`],
      ['jobs', '/jobs'],
      ['settings', '/settings'],
    ]
    for (const [name, path] of screens) {
      await page.goto(path)
      await expect(page.locator('#main')).toBeVisible()
      await axe(page, name)
    }
    // The workbench's other views.
    await page.goto(`/projects/${p.id}/mappings/${ids[0]}`)
    for (const view of ['Composer', 'Design']) {
      await page.getByRole('tab', { name: view }).click()
      await axe(page, `mapping workbench (${view})`)
    }

    await dropScope(scope)
    await api.dispose()
  })
}
