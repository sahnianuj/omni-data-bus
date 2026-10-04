import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import axe from 'axe-core'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { routes } from '@/app/routes'
import { buildInfoText, staleVersion } from '@/features/system/buildInfo'
import { sampleVersion, server, setupMswServer } from '@/test/msw'
import { renderRoutes, renderUI } from '@/test/render'
import { mockSession } from '@/test/session'
import { BuildInfoFooter } from './BuildInfoFooter'

setupMswServer()
beforeEach(() => mockSession())
afterEach(() => vi.unstubAllEnvs())

const footer = () => screen.findByRole('contentinfo', { name: 'Build information' })

function serve(over: Parameters<typeof sampleVersion>[0] = {}) {
  server.use(http.get('*/api/v1/version', () => HttpResponse.json(sampleVersion(over))))
}

describe('BuildInfoFooter', () => {
  it('AC3: renders release, commit, date', async () => {
    renderUI(<BuildInfoFooter />)
    const f = await footer()
    expect(
      await within(f).findByText('Omni-Data-Bus 2.0.0 · build 3f2a9c1 · Nov 2, 2026'),
    ).toBeInTheDocument()
    // The full UTC timestamp is in the tooltip.
    expect(within(f).getByRole('button')).toHaveAttribute('title', '2026-11-02T14:05:11Z')
    expect(within(f).queryByText('DEV')).not.toBeInTheDocument()
  })

  it('OC-072 AC1: names the bundled Couchbase in the all-in-one image', async () => {
    serve({ bundledStore: 'Couchbase Server CE 8.0.2' })
    renderUI(<BuildInfoFooter />)
    const f = await footer()
    expect(await within(f).findByText(/· Couchbase Server CE 8\.0\.2 \(bundled\)$/)).toBeInTheDocument()
    expect(buildInfoText(sampleVersion({ bundledStore: 'Couchbase Server CE 8.0.2' }), 'UA')).toContain('Bundled: Couchbase Server CE 8.0.2')
  })

  it('AC3: sign-in page shows the release only', async () => {
    mockSession(null)
    server.use(
      http.get('*/api/v1/version', () => HttpResponse.json({ version: '2.0.0', channel: 'release' })),
    )
    renderRoutes(routes, '/signin')
    const f = await footer()
    expect(await within(f).findByText('Omni-Data-Bus 2.0.0')).toBeInTheDocument()
    expect(within(f).queryByRole('button')).not.toBeInTheDocument()
    expect(screen.getByText('Release 2.0.0')).toBeInTheDocument()
  })

  it('AC3: renders nothing but the landmark when the server hides the version', async () => {
    mockSession(null)
    server.use(http.get('*/api/v1/version', () => HttpResponse.json({})))
    renderRoutes(routes, '/signin')
    await screen.findByRole('heading', { name: /sign in/i })
    expect((await footer()).textContent).toBe('')
  })

  it('AC3: copy build info', async () => {
    const user = userEvent.setup()
    const writeText = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue()
    renderUI(<BuildInfoFooter />)
    await user.click(await within(await footer()).findByRole('button'))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('go1.26.8')).toBeInTheDocument()
    expect(within(dialog).getByRole('link', { name: /release notes/i })).toHaveAttribute(
      'href',
      'https://github.com/sahnianuj/omni-data-bus/releases/tag/v2.0.0',
    )
    await user.click(within(dialog).getByRole('button', { name: 'Copy build info' }))
    expect(writeText).toHaveBeenCalledWith(
      'Omni-Data-Bus 2.0.0 (release)\n' +
        'Commit: 3f2a9c1  Built: 2026-11-02T14:05:11Z  Go: go1.26.8  Platform: linux/amd64\n' +
        `Metadata schema: 7   Nodes: api 2.0.0, worker 2.0.0   Browser: ${navigator.userAgent}\n`,
    )
  })

  it('AC4: mixed versions badge', async () => {
    const user = userEvent.setup()
    serve({ nodes: { api: ['2.0.0'], worker: ['1.0.1'] }, consistent: false })
    renderUI(<BuildInfoFooter />)
    const f = await footer()
    expect(await within(f).findByText('Mixed versions')).toBeInTheDocument()
    await user.click(within(f).getByRole('button'))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText(/running different versions/)).toBeInTheDocument()
    expect(within(dialog).getByText('1.0.1')).toBeInTheDocument()
    expect(within(dialog).getAllByText('2.0.0').length).toBeGreaterThan(0)
  })

  it('AC5: stale tab reload prompt', async () => {
    vi.stubEnv('VITE_ODB_VERSION', 'v2.0.0')
    serve({ version: '2.0.1', nodes: { api: ['2.0.1'], worker: ['2.0.1'] } })
    const reload = vi.fn()
    vi.spyOn(window, 'location', 'get').mockReturnValue({ ...window.location, reload })
    renderUI(<BuildInfoFooter />)
    const prompt = await screen.findByRole('status')
    expect(prompt).toHaveTextContent('A new version (2.0.1) is available')
    await userEvent.click(within(prompt).getByRole('button', { name: 'Reload' }))
    expect(reload).toHaveBeenCalled()
  })

  it('AC5: no prompt when versions match or the SPA is a dev build', async () => {
    vi.stubEnv('VITE_ODB_VERSION', '2.0.0')
    renderUI(<BuildInfoFooter />)
    await within(await footer()).findByText(/Omni-Data-Bus 2\.0\.0/)
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
    expect(staleVersion('2.0.1', 'dev')).toBeNull()
    expect(staleVersion(undefined, '2.0.0')).toBeNull()
    expect(staleVersion('v2.0.1', '2.0.0')).toBe('2.0.1')
  })

  it.each([
    ['dev', { channel: 'dev' as const, version: 'dev', commit: 'none', buildTime: '' }, 'DEV', false],
    ['rc', { channel: 'rc' as const, version: '2.0.0-rc.1' }, 'RC', true],
    ['dirty', { channel: 'dev' as const, version: '2.0.0-3-g3f2a9c1-dirty', dirty: true }, 'dirty', false],
  ])('AC6: badges and axe (%s)', async (_name, over, badge, releaseNotes) => {
    const user = userEvent.setup()
    serve(over)
    renderUI(<BuildInfoFooter />)
    const f = await footer()
    expect(await within(f).findByText(badge)).toBeInTheDocument()
    await user.click(within(f).getByRole('button'))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).queryByRole('link', { name: /release notes/i }) !== null).toBe(releaseNotes)
    for (const dark of [false, true]) {
      document.documentElement.classList.toggle('dark', dark)
      const res = await axe.run(document.body, {
        rules: { 'color-contrast': { enabled: false }, region: { enabled: false } },
      })
      expect(
        res.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical').map((v) => v.id),
      ).toEqual([])
    }
  })

  it('buildInfoText without full details (anonymous) has only name and browser', () => {
    expect(buildInfoText({ version: '2.0.0', channel: 'release' }, 'UA')).toBe(
      'Omni-Data-Bus 2.0.0 (release)\nBrowser: UA\n',
    )
  })
})
