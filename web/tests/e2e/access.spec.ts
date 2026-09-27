import { expect, test } from '@playwright/test'
import { Api } from './support/api'
import { moveMembership } from './support/db'
import { SignIn } from './support/pages'

test('AC7: viewer read-only; cross-workspace 404', async ({ page }) => {
  const owner = await Api.signUp('Owner')
  const p = await owner.projectWithRepos()
  const { ids } = await owner.mappings(p.id, ['shop.customer'])

  // A second user joins the owner's workspace as a viewer (there is no invite flow in 1.0).
  const signup = await Api.signUp('Viewer')
  await signup.dispose()
  await moveMembership(signup.userId, owner.workspaceId, 'viewer')
  const viewer = await Api.signIn(signup.email)
  expect(viewer.workspaceId).toBe(owner.workspaceId)

  // The viewer sees everything…
  await new SignIn(page).signIn(signup.email)
  await expect(page.getByText('Viewer', { exact: true }).first()).toBeVisible()
  await page.goto('/projects')
  await page.getByRole('link', { name: new RegExp((await owner.get<{ name: string }>(`/api/v1/projects/${p.id}`)).name) }).first().click()
  await expect(page).toHaveURL(new RegExp(`/projects/${p.id}/`))
  await page.goto(`/projects/${p.id}/mappings/${ids[0]}`)
  await expect(page.getByText(/auto-mapped \d+ of \d+ columns/).first()).toBeVisible()

  // …but can't change or run anything: the UI disables the actions…
  await page.goto(`/projects/${p.id}/review`)
  await expect(page.getByRole('button', { name: 'Migrate', exact: true })).toBeDisabled()
  await expect(page.getByRole('textbox', { name: 'Project name' })).not.toBeEditable()

  // …and the API refuses them.
  expect(await viewer.status('POST', `/api/v1/projects/${p.id}/jobs`, {})).toBe(403)
  expect(await viewer.status('PATCH', `/api/v1/projects/${p.id}`, { name: 'Renamed' })).toBe(403)
  expect(await viewer.status('DELETE', `/api/v1/projects/${p.id}`)).toBe(403)
  expect(await viewer.status('POST', `/api/v1/connections/${encodeURIComponent(p.sourceId)}/test`)).toBe(403)
  expect(await viewer.status('GET', `/api/v1/projects/${p.id}`)).toBe(200)

  // Someone in another workspace can't even see that the project exists.
  const stranger = await Api.signUp('Stranger')
  for (const path of [`/api/v1/projects/${p.id}`, `/api/v1/projects/${p.id}/mappings/${ids[0]}`, `/api/v1/connections/${encodeURIComponent(p.sourceId)}`]) {
    expect(await stranger.status('GET', path), path).toBe(404)
  }
  await page.getByRole('button', { name: 'Sign out' }).click()
  await new SignIn(page).signIn(stranger.email)
  await page.goto(`/projects/${p.id}/source`)
  await expect(page.getByText(/not found|doesn't exist|no longer exists/i).first()).toBeVisible()

  await Promise.all([owner.dispose(), viewer.dispose(), stranger.dispose()])
})
