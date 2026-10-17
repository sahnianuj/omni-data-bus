import { screen, within } from '@testing-library/react'
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
  embeddable: ['linux_amd64'], maxPerHour: 10,
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

function setup(project = makeProject({ id: 'p1', name: 'Shop', source: repo('postgres'), target: repo('couchbase') }), headers: Record<string, string> = {}) {
  mockSession('editor')
  const requests: Record<string, unknown>[] = []
  server.use(
    http.get('*/api/v1/projects/p1/mappings', () =>
      HttpResponse.json({ items: [summary('m1', 'orders'), summary('m2', 'customers'), summary('m3', 'refunds', { status: 'draft', errors: 1, firstError: { code: 'X', message: 'Key template uses a missing column' } })] }),
    ),
    http.get('*/api/v1/exports/options', () => HttpResponse.json(options)),
    http.post('*/api/v1/projects/p1/exports', async ({ request }) => {
      requests.push((await request.json()) as Record<string, unknown>)
      return new HttpResponse(new Blob(['zip']), {
        status: 201,
        headers: { 'Content-Type': 'application/zip', 'Content-Disposition': 'attachment; filename="odb-export-shop-20261001.zip"', 'X-Odb-Bundle-Sha256': 'f'.repeat(64), 'X-Odb-Export-Id': 'e1', ...headers },
      })
    }),
  )
  renderUI(<ExportDialog project={project} open onOpenChange={() => {}} />)
  return { requests }
}

describe('ExportDialog', () => {
  it('exports in two steps without asking for a passphrase or password', async () => {
    const user = userEvent.setup()
    const { requests } = setup()
    // 1. The project as configured, read-only, each part linking to its step.
    expect(within(screen.getByRole('list', { name: 'Export steps' })).getAllByRole('listitem').map((li) => li.textContent)).toEqual(['1. Project', '2. Options'])
    const maps = await screen.findByRole('region', { name: /Mappings: 2 of 3 ready/ })
    expect(within(maps).getByText('public.refunds').closest('li')).toHaveTextContent('Not ready — not exported')
    expect(within(maps).getByRole('link', { name: 'Change in Design & map' })).toHaveAttribute('href', '/projects/p1/mappings')
    expect(within(screen.getByRole('region', { name: 'Target' })).getByRole('link', { name: /Change in/ })).toHaveAttribute('href', '/projects/p1/target')
    expect(screen.getByRole('region', { name: 'Credentials' })).toHaveTextContent('in config.json beside the script')
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
    expect(screen.queryByRole('radio')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Next' }))
    // 2. Options: no bundle-or-script choice, odb not included by default; Export downloads at once.
    expect(screen.queryByRole('radio')).not.toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: /Include odb/ })).not.toBeChecked()
    expect(screen.queryByLabelText(/passphrase/i)).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Export' }))

    await screen.findByText('Export downloaded')
    expect(screen.queryByLabelText(/password/i)).not.toBeInTheDocument()
    expect(requests).toHaveLength(1)
    expect(requests[0]).toHaveProperty('jobOptions')
    for (const k of ['passphrase', 'mappingIds', 'targetOverride', 'credentialMode', 'refs', 'runtime', 'script', 'includeOdb']) expect(requests[0]).not.toHaveProperty(k)
    expect(saved).toEqual(['odb-export-shop-20261001.zip'])
    const files = screen.getByLabelText('Files in the export')
    expect(within(files).getAllByRole('term').map((d) => d.textContent)).toEqual(['migrate.sh', 'config.json', 'mappings.json', 'README.md'])
    expect(screen.getByRole('note')).toHaveTextContent('config.json holds the database credentials')
  })

  it('OC-160 AC1: one download with an optional odb', async () => {
    const user = userEvent.setup()
    const { requests } = setup(undefined, { 'X-Odb-Included': 'linux_amd64' })
    await screen.findByRole('region', { name: /Mappings/ })
    await user.click(screen.getByRole('button', { name: 'Next' }))
    await user.click(screen.getByRole('checkbox', { name: /Include odb/ }))
    // Only platforms this server has can be picked.
    const machine = screen.getByLabelText('Machine')
    expect(within(machine).getByRole('option', { name: 'linux / amd64' })).toBeEnabled()
    expect(within(machine).getByRole('option', { name: 'darwin / arm64' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Export' }))
    await screen.findByText('Export downloaded')
    expect(requests[0]).toMatchObject({ includeOdb: { os: 'linux', arch: 'amd64' } })
    expect(within(screen.getByLabelText('Files in the export')).getAllByRole('term').map((d) => d.textContent)).toContain('odb')
  })

  it('shows a failed export in the dialog', async () => {
    const user = userEvent.setup()
    setup()
    server.use(http.post('*/api/v1/projects/p1/exports', () => HttpResponse.json({ title: 'Too many exports', detail: 'At most 10 exports an hour.', code: 'RATE_LIMITED', status: 429 }, { status: 429 })))
    await screen.findByRole('region', { name: /Mappings/ })
    await user.click(screen.getByRole('button', { name: 'Next' }))
    await user.click(screen.getByRole('button', { name: 'Export' }))
    expect(await screen.findByText(/At most 10 exports an hour/)).toBeInTheDocument()
    expect(saved).toEqual([])
  })

  it("can't export the bundled Couchbase: points to the Target step", async () => {
    setup(makeProject({ id: 'p1', name: 'Shop', source: repo('postgres'), target: { ...repo('couchbase'), name: 'Bundled Couchbase' } }))
    expect(await screen.findByRole('alert')).toHaveTextContent('choose another target in the Target step')
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled()
  })
})
