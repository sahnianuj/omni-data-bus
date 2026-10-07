import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { components } from '@/lib/api/schema'
import { server, setupMswServer } from '@/test/msw'
import { makeProject, repo } from '@/test/projects'
import { renderUI } from '@/test/render'
import { mockSession } from '@/test/session'
import { ExportDialog } from './ExportDialog'

type MappingSummary = components['schemas']['MappingSummary']

setupMswServer()

const summary = (id: string, table: string, over: Partial<MappingSummary> = {}): MappingSummary =>
  ({
    id, sourceSchema: 'public', sourceTable: table, mode: 'canvas', driving: { schema: 'public', name: table },
    target: { bucket: 'shop', scope: 'sales', collection: table }, location: { kind: 'couchbase', bucket: 'shop', scope: 'sales', collection: table },
    status: 'ready', stats: { totalSourceColumns: 3, autoMapped: 3, manual: 0, suggested: 0, unmapped: 0 }, version: 1,
    updatedAt: new Date().toISOString(), errors: 0, warnings: 0, ...over,
  }) as MappingSummary

const options = {
  version: '2.0.0', allowPlaintext: true, image: 'ghcr.io/x/omni-data-bus:2.0.0', downloadUrl: 'https://example.com', downloadable: ['linux_amd64'],
  embeddable: ['linux_amd64'], maxPerHour: 10, minPassphraseLength: 12, stepUpWindowSec: 300,
}

let saved: string[]
beforeEach(() => {
  saved = []
  vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: () => 'blob:x', revokeObjectURL: () => {} }))
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
    saved.push(this.download)
  })
})
afterEach(() => vi.restoreAllMocks())

function setup(stepUpFirst = false, project = makeProject({ id: 'p1', name: 'Shop', source: repo('postgres'), target: repo('couchbase') })) {
  mockSession('editor')
  const requests: Record<string, unknown>[] = []
  const stepUps: string[] = []
  let steppedUp = !stepUpFirst
  server.use(
    http.get('*/api/v1/projects/p1/mappings', () =>
      HttpResponse.json({ items: [summary('m1', 'orders'), summary('m2', 'customers'), summary('m3', 'refunds', { status: 'draft', errors: 1, firstError: { code: 'X', message: 'Key template uses a missing column' } })] }),
    ),
    http.get('*/api/v1/exports/options', () => HttpResponse.json(options)),
    http.post('*/api/v1/exports/passphrase', () => HttpResponse.json({ passphrase: 'acid-bloom-cradle-dwell-ember-fjord' })),
    http.post('*/api/v1/auth/step-up', async ({ request }) => {
      stepUps.push(((await request.json()) as { password: string }).password)
      steppedUp = true
      return new HttpResponse(null, { status: 204 })
    }),
    http.post('*/api/v1/projects/p1/exports', async ({ request }) => {
      requests.push((await request.json()) as Record<string, unknown>)
      if (!steppedUp) return HttpResponse.json({ title: 'Confirm', code: 'STEP_UP_REQUIRED', status: 403 }, { status: 403 })
      return new HttpResponse(new Blob(['zip']), {
        status: 201,
        headers: { 'Content-Type': 'application/zip', 'Content-Disposition': 'attachment; filename="odb-export-shop-20261001.zip"', 'X-Odb-Bundle-Sha256': 'f'.repeat(64), 'X-Odb-Export-Id': 'e1' },
      })
    }),
  )
  renderUI(<ExportDialog project={project} open onOpenChange={() => {}} />)
  return { requests, stepUps }
}

describe('ExportDialog', () => {
  it('AC1: full flow produces request', async () => {
    const user = userEvent.setup()
    const { requests } = setup()
    // 1. The project as configured, read-only, each part linking to its step.
    const maps = await screen.findByRole('region', { name: /Mappings: 2 of 3 ready/ })
    expect(within(maps).getByText('public.refunds').closest('li')).toHaveTextContent('Not ready — not exported')
    expect(within(maps).getByRole('link', { name: 'Change in Design & map' })).toHaveAttribute('href', '/projects/p1/mappings')
    expect(within(screen.getByRole('region', { name: 'Target' })).getByRole('link', { name: /Change in/ })).toHaveAttribute('href', '/projects/p1/target')
    expect(screen.getByRole('region', { name: 'Credentials' })).toHaveTextContent('saved source and target credentials, sealed with your passphrase')
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
    expect(screen.queryByRole('radio')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Next' }))
    // 2. The passphrase, generated.
    await user.click(screen.getByRole('button', { name: 'Generate' }))
    await waitFor(() => expect(screen.getByLabelText('Passphrase')).toHaveValue('acid-bloom-cradle-dwell-ember-fjord'))
    await user.click(screen.getByRole('button', { name: 'Next' }))
    // 3. Runtime: the script.
    expect(screen.getByRole('radio', { name: /migrate.sh script/ })).toBeChecked()
    await user.click(screen.getByRole('button', { name: 'Export' }))

    await screen.findByText('Export downloaded')
    expect(requests).toHaveLength(1)
    expect(requests[0]).toMatchObject({ passphrase: 'acid-bloom-cradle-dwell-ember-fjord', runtime: 'script', script: { runtime: 'auto' } })
    for (const k of ['mappingIds', 'targetOverride', 'credentialMode', 'refs']) expect(requests[0]).not.toHaveProperty(k)
    expect(saved).toEqual(['odb-export-shop-20261001.zip'])
    expect(screen.getByText('Passphrase shown only once. Store it in your secret manager.')).toBeInTheDocument()
    expect(screen.getByText('acid-bloom-cradle-dwell-ember-fjord')).toBeInTheDocument()
  })

  it('asks for the password when the server needs a step-up, then exports', async () => {
    const user = userEvent.setup()
    const { requests, stepUps } = setup(true)
    await screen.findByRole('region', { name: /Mappings/ })
    await user.click(screen.getByRole('button', { name: 'Next' }))
    await user.type(screen.getByLabelText('Passphrase'), 'correct-horse-battery-staple')
    await user.click(screen.getByRole('button', { name: 'Next' }))
    await user.click(screen.getByRole('button', { name: 'Export' }))
    const pw = await screen.findByLabelText(/Confirm your password/)
    await user.type(pw, 'my-password')
    await user.click(screen.getByRole('button', { name: 'Confirm and export' }))
    await screen.findByText('Export downloaded')
    expect(stepUps).toEqual(['my-password'])
    expect(requests).toHaveLength(2)
  })

  it('checks the passphrase before moving on', async () => {
    const user = userEvent.setup()
    setup()
    await screen.findByRole('region', { name: /Mappings/ })
    await user.click(screen.getByRole('button', { name: 'Next' }))
    await user.type(screen.getByLabelText('Passphrase'), 'password')
    await user.click(screen.getByRole('button', { name: 'Next' }))
    expect(screen.getByRole('alert')).toHaveTextContent('at least 12 characters')
  })

  it("can't export the bundled Couchbase: points to the Target step", async () => {
    setup(false, makeProject({ id: 'p1', name: 'Shop', source: repo('postgres'), target: { ...repo('couchbase'), name: 'Bundled Couchbase' } }))
    expect(await screen.findByRole('alert')).toHaveTextContent('choose another target in the Target step')
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled()
  })
})
