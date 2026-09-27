import { expect, test } from '@playwright/test'
import { Api } from './support/api'
import { docCount, docker, dropScope } from './support/db'
import { env } from './support/env'
import { Monitor, SignIn } from './support/pages'

// shop.big_events has 1,000,000 rows (deploy/compose/seed/source/02-big-events.sql). The job is
// throttled so there is time to act mid-run.
const ROWS = 1_000_000
const THROTTLE = { maxDocsPerSecond: 40_000 }

test('AC4: pause/resume 1M rows', async ({ page }) => {
  test.setTimeout(300_000)
  const api = await Api.signUp()
  const p = await api.projectWithRepos()
  const { scope } = await api.mappings(p.id, ['shop.big_events'])
  await new SignIn(page).signIn(api.email)
  const job = await api.startJob(p.id, THROTTLE)
  const monitor = new Monitor(page)
  await monitor.open(p.id, job.id)

  await api.waitWritten(job.id, 100_000)
  await page.getByRole('button', { name: 'Pause' }).click()
  await expect(page.getByRole('button', { name: 'Resume' })).toBeVisible({ timeout: 60_000 })
  const paused = await api.waitJob(job.id, ['paused'])
  expect(paused.progress.written).toBeLessThan(ROWS)

  // Paused means nothing is written any more: the collection settles at exactly what the job
  // reports written (COUNT(*) reads the collection's item count, which trails writes briefly)…
  await expect.poll(() => docCount(scope, 'big_events'), { timeout: 30_000 }).toBe(paused.progress.written)
  // …and stays there.
  await page.waitForTimeout(3000)
  expect(await docCount(scope, 'big_events')).toBe(paused.progress.written)
  expect((await api.job(job.id)).progress.written).toBe(paused.progress.written)

  await page.getByRole('button', { name: 'Resume' }).click()
  await monitor.waitFinished(240_000)
  await expect(monitor.progress()).toHaveAttribute('aria-valuenow', '100')
  const done = await api.waitJob(job.id, ['completed'])
  expect(done.progress.written).toBe(ROWS)
  expect(await docCount(scope, 'big_events')).toBe(ROWS)

  await dropScope(scope)
  await api.dispose()
})

test('AC4: worker killed mid-run resumes with no loss', async ({ page }) => {
  test.setTimeout(420_000)
  const api = await Api.signUp()
  const p = await api.projectWithRepos()
  const { scope } = await api.mappings(p.id, ['shop.big_events'])
  const job = await api.startJob(p.id, THROTTLE)
  await api.waitWritten(job.id, 150_000)

  // Crash: no drain, no final checkpoint. The restarted worker's reaper requeues the job once its
  // heartbeat is stale (30 s), and the job resumes from the last persisted checkpoint.
  docker('kill', env.workerContainer)
  try {
    const crashed = await api.job(job.id)
    expect(crashed.progress.written).toBeLessThan(ROWS)
  } finally {
    docker('start', env.workerContainer)
  }

  const done = await api.waitJob(job.id, ['completed'], 300_000)
  expect(done.progress.written).toBeGreaterThanOrEqual(ROWS) // at-least-once: replayed rows may count twice
  expect(await docCount(scope, 'big_events')).toBe(ROWS) // …but every key is there exactly once

  const log = JSON.stringify(await api.get(`/api/v1/jobs/${job.id}/events/log`))
  expect(log).toMatch(/Resumed from checkpoint/i)

  await new SignIn(page).signIn(api.email)
  const monitor = new Monitor(page)
  await monitor.open(p.id, job.id)
  await monitor.waitFinished()
  await expect(monitor.summary().getByText('Passed', { exact: true })).toBeVisible({ timeout: 60_000 })

  await dropScope(scope)
  await api.dispose()
})
