import { expect, test } from '@playwright/test'
import { Api } from './support/api'
import { dropScope } from './support/db'
import { SignIn } from './support/pages'

// OC-058 AC6: the build footer is on every page and never covers the mapping canvas.
test('AC6: build footer visible, canvas not overlapped', { tag: '@smoke' }, async ({ page }) => {
  await page.goto('/signin')
  const footer = page.getByRole('contentinfo', { name: 'Build information' })
  // Anonymous: the release only, no build details to open.
  await expect(footer).toContainText(/Omni-Data-Bus \S+/)
  await expect(footer.getByRole('button')).toHaveCount(0)

  const api = await Api.signUp()
  const p = await api.projectWithRepos()
  const { scope, ids } = await api.mappings(p.id, ['shop.customer'])
  await new SignIn(page).signIn(api.email)
  await page.goto(`/projects/${p.id}/mappings/${ids[0]}`)
  await expect(page.getByRole('region', { name: 'Related tables' })).toBeVisible()

  await expect(footer).toContainText(/Omni-Data-Bus \S+ · build \S+/)
  const f = await footer.boundingBox()
  const main = await page.locator('#main').boundingBox()
  const viewport = page.viewportSize()
  expect(f && main && viewport).toBeTruthy()
  // Pinned to the bottom of the viewport, and the content area ends where the footer starts.
  expect(Math.round(f!.y + f!.height)).toBe(viewport!.height)
  expect(main!.y + main!.height).toBeLessThanOrEqual(f!.y + 0.5)

  await footer.getByRole('button').click()
  const about = page.getByRole('dialog', { name: 'About this build' })
  await expect(about.getByRole('button', { name: 'Copy build info' })).toBeVisible()
  await expect(about.getByText('Metadata schema')).toBeVisible()
  await dropScope(scope)
})
