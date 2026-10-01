import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { useState } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { routes } from '@/app/routes'
import { tbl } from '@/test/discovery'
import { withFixedLayout } from '@/test/layout'
import { columns, mappingView, mockMapping, sampleSpec, stats } from '@/test/mapping'
import { server, setupMswServer } from '@/test/msw'
import { makeProject, mockProject, repo } from '@/test/projects'
import { renderRoutes, renderUI } from '@/test/render'
import { mockSession } from '@/test/session'
import { MappingScoreHeader, SegmentBar } from './MappingScoreHeader'

setupMswServer()
withFixedLayout(320, 600)
beforeEach(() => mockSession('owner'))

describe('MappingScoreHeader', () => {
  it('AC1: headline text and percent', () => {
    renderUI(<MappingScoreHeader automap={stats()} live={stats()} onFilter={() => {}} onRerun={() => {}} />)
    expect(screen.getByRole('heading', { name: 'Omni-Data-Bus auto-mapped 10 of 23 columns' })).toBeInTheDocument()
    expect(screen.getByRole('img', { name: '43% auto-mapped' })).toBeInTheDocument()
    expect(screen.getByText('Average confidence 94%')).toBeInTheDocument()
  })

  it('viewers see the re-run controls disabled', async () => {
    mockSession('viewer')
    renderUI(<MappingScoreHeader automap={stats()} live={stats()} onFilter={() => {}} onRerun={() => {}} />)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Re-run auto-map' })).toBeDisabled())
    expect(screen.getByRole('button', { name: 'Auto-map options' })).toBeDisabled()
  })

  it('AC1: pluralization and zero state', () => {
    const { unmount } = renderUI(
      <MappingScoreHeader automap={stats({ totalSourceColumns: 1, autoMapped: 1 })} live={stats({ requiredTargetUnmapped: 1 })} onFilter={() => {}} onRerun={() => {}} />,
    )
    expect(screen.getByRole('heading', { name: 'Omni-Data-Bus auto-mapped 1 of 1 column' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '1 required field unmapped' })).toBeInTheDocument()
    unmount()
    renderUI(<MappingScoreHeader automap={stats({ totalSourceColumns: 0, autoMapped: 0 })} live={stats()} onFilter={() => {}} onRerun={() => {}} />)
    expect(screen.getByRole('heading', { name: 'This table has no columns to map.' })).toBeInTheDocument()
  })

  it('AC3: announces coverage change', async () => {
    function Harness() {
      const [live, setLive] = useState(stats())
      return (
        <>
          <MappingScoreHeader automap={stats()} live={live} onFilter={() => {}} onRerun={() => {}} />
          <button type="button" onClick={() => setLive(stats({ manuallyMapped: 4, coverageRatio: 14 / 23 }))}>
            edit
          </button>
        </>
      )
    }
    renderUI(<Harness />)
    const live = screen.getByText(/coverage/).closest('[aria-live]')
    expect(live).toHaveAttribute('aria-live', 'polite')
    expect(live).toHaveTextContent('57% coverage (13 of 23 columns)')
    await userEvent.click(screen.getByRole('button', { name: 'edit' }))
    expect(live).toHaveTextContent('61% coverage (14 of 23 columns)')
  })

  it('AC7: reset asks for confirmation, then re-runs in reset mode', async () => {
    const onRerun = vi.fn()
    renderUI(<MappingScoreHeader automap={stats()} live={stats()} onFilter={() => {}} onRerun={onRerun} />)
    // Wait for the session: until then, editor controls are wrapped read-only.
    await waitFor(() => expect(screen.getByRole('button', { name: 'Re-run auto-map' }).closest('span[tabindex="0"]')).toBeNull())
    await userEvent.click(screen.getByRole('button', { name: 'Re-run auto-map' }))
    expect(onRerun).toHaveBeenLastCalledWith('fill')
    await userEvent.click(screen.getByRole('button', { name: 'Auto-map options' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: 'Reset auto and suggested mappings' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Reset' }))
    expect(onRerun).toHaveBeenLastCalledWith('reset')
  })
})

describe('SegmentBar', () => {
  it('AC2: proportional widths', () => {
    renderUI(<SegmentBar stats={stats()} onFilter={() => {}} />)
    expect(screen.getByTestId('segment-auto').style.width).toBe(`${(10 / 23) * 100}%`)
    expect(screen.getByTestId('segment-unmapped').style.width).toBe(`${(6 / 23) * 100}%`)
    expect(screen.queryByTestId('segment-ignored')).not.toBeInTheDocument()
  })

  it('AC2: click sets filter in store', async () => {
    const onFilter = vi.fn()
    renderUI(<SegmentBar stats={stats()} onFilter={onFilter} />)
    await userEvent.click(screen.getByTestId('segment-suggested'))
    expect(onFilter).toHaveBeenLastCalledWith('suggested')
    await userEvent.click(screen.getByText('6 unmapped'))
    expect(onFilter).toHaveBeenLastCalledWith('unmapped')
    await userEvent.click(screen.getByText('1 ignored'))
    expect(onFilter).toHaveBeenCalledTimes(2)
  })

  it('AC7: reset calls API and refreshes', async () => {
    mockProject(makeProject({ source: repo('postgres'), target: repo('couchbase'), mappings: 1, readyMappings: 0 }))
    server.use(
      http.get('*/api/v1/connections/:id/schema/:schema/:table', () => HttpResponse.json(tbl('customer', { columns }))),
      http.get('*/api/v1/projects/:pid/mappings', () => HttpResponse.json({ items: [] })),
    )
    mockMapping()
    // The server recomputes auto/suggested mappings (age now auto-mapped) and keeps the manual one.
    const spec = sampleSpec()
    spec.fieldMappings.push({ id: 'm_age', targetFieldId: 'f_age', sources: [{ column: 'age_text' }], transforms: [{ op: 'parseInteger' }], origin: 'auto', state: 'active', confidence: 0.86 })
    const calls: { ifMatch: string | null; body: unknown }[] = []
    server.use(
      http.post('*/api/v1/projects/:pid/mappings/:mid/automap', async ({ request }) => {
        calls.push({ ifMatch: request.headers.get('If-Match'), body: await request.json() })
        return HttpResponse.json(mappingView({ spec, version: 4, automapStats: stats({ autoMapped: 12, autoMapRatio: 12 / 23 }) }))
      }),
    )
    // In the field list (Table view, on demand since OC-067), which shows each field's source.
    renderRoutes(routes, '/projects/p1/mappings/m1?view=table')
    expect(await screen.findByRole('heading', { name: 'Omni-Data-Bus auto-mapped 10 of 23 columns' })).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Auto-map options' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: 'Reset auto and suggested mappings' }))
    const dialog = await screen.findByRole('dialog', { name: 'Reset auto-mapping?' })
    expect(dialog).toHaveTextContent('Your manual mappings are kept.')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Reset' }))

    expect(await screen.findByRole('heading', { name: 'Omni-Data-Bus auto-mapped 12 of 23 columns' })).toBeInTheDocument()
    expect(calls).toEqual([{ ifMatch: '"3"', body: { mode: 'reset' } }])
    expect(await screen.findByRole('combobox', { name: 'Source for age' })).toHaveValue('age_text')
    expect(screen.getByRole('combobox', { name: 'Source for createdAt' })).toHaveValue('created_on')
  })
})
