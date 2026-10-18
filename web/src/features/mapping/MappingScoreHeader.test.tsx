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
  it('AC1 / OC-178 AC2: the wheel and headline are the fields done; auto-mapping is the detail', async () => {
    const onFilter = vi.fn()
    renderUI(<MappingScoreHeader automap={stats()} live={stats()} onFilter={onFilter} onRerun={() => {}} />)
    expect(screen.getByRole('heading', { name: '13 of 20 target fields done' })).toBeInTheDocument()
    expect(screen.getByRole('img', { name: "65% of the document's fields done: 50% filled with data, 15% not populated" })).toBeInTheDocument()
    expect(screen.getByTestId('done-split')).toHaveTextContent('10 filled with data·3 not populated·7 to go (2 required)Show them')
    await userEvent.click(screen.getByRole('button', { name: 'Show them' }))
    expect(onFilter).toHaveBeenCalledWith('unmapped')
    expect(screen.getByText('Source columns · auto-mapped 10 of 23 on the last run')).toBeInTheDocument()
    expect(screen.getByText('Average confidence 94%')).toBeInTheDocument()
  })

  it('OC-187 AC3: filled apart from left empty; one line for the source columns used, embedded included', () => {
    const live = stats({
      targetFields: 100, targetFieldsDone: 98, doneRatio: 0.98, targetFieldsFilled: 73, targetFieldsNotPopulated: 25, requiredTargetUnmapped: 1,
      totalSourceColumns: 28, autoMapped: 18, manuallyMapped: 0, suggested: 0, unmappedSource: 10,
      embedded: { tables: 3, totalColumns: 84, autoMapped: 50, manuallyMapped: 2, suggested: 0, unmappedSource: 32 },
    })
    renderUI(<MappingScoreHeader automap={live} live={live} onFilter={() => {}} onRerun={() => {}} />)
    expect(screen.getByRole('heading', { name: '98 of 100 target fields done' })).toBeInTheDocument()
    expect(screen.getByRole('img', { name: "98% of the document's fields done: 73% filled with data, 25% not populated" })).toBeInTheDocument()
    expect(screen.getByTestId('done-split')).toHaveTextContent('73 filled with data·25 not populated·2 to go (1 required)Show them')
    expect(screen.getByTestId('source-used')).toHaveTextContent('Source columns used: 70 of 112 (18 of 28 from the table, 52 of 84 embedded)')
    expect(screen.getByText('10 not used')).toBeInTheDocument()
    expect(screen.queryByText(/coverage/)).toBeNull()
  })

  it('OC-187: nothing left empty and nothing to go: only the filled count', () => {
    const live = stats({ targetFields: 20, targetFieldsDone: 20, doneRatio: 1, targetFieldsFilled: 20, targetFieldsNotPopulated: 0 })
    renderUI(<MappingScoreHeader automap={live} live={live} onFilter={() => {}} onRerun={() => {}} />)
    expect(screen.getByRole('img', { name: "100% of the document's fields done" })).toBeInTheDocument()
    expect(screen.getByTestId('done-split')).toHaveTextContent(/^20 filled with data$/)
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
    expect(screen.getByText('Source column · auto-mapped 1 of 1 on the last run')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '1 required field unmapped' })).toBeInTheDocument()
    unmount()
    const again = renderUI(<MappingScoreHeader automap={stats()} live={stats({ targetFields: 20, targetFieldsDone: 20, doneRatio: 1 })} onFilter={() => {}} onRerun={() => {}} />)
    expect(screen.getByRole('heading', { name: 'All 20 target fields done' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Show them' })).toBeNull()
    again.unmount()
    renderUI(<MappingScoreHeader automap={stats({ totalSourceColumns: 0, autoMapped: 0 })} live={stats({ targetFields: 0, targetFieldsDone: 0, doneRatio: 0 })} onFilter={() => {}} onRerun={() => {}} />)
    expect(screen.getByRole('heading', { name: 'The document has no fields yet' })).toBeInTheDocument()
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
    const live = screen.getByTestId('source-used').closest('[aria-live]')
    expect(live).toHaveAttribute('aria-live', 'polite')
    expect(live).toHaveTextContent('Source columns used: 13 of 23')
    await userEvent.click(screen.getByRole('button', { name: 'edit' }))
    expect(live).toHaveTextContent('Source columns used: 14 of 23')
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
    await userEvent.click(screen.getByText('6 not used'))
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
    // The live count: customerId, name.first and createdAt of 7 fields (email is only suggested).
    expect(await screen.findByRole('heading', { name: '3 of 7 target fields done' })).toBeInTheDocument()
    expect(screen.getByText('Source columns · auto-mapped 10 of 23 on the last run')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Auto-map options' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: 'Reset auto and suggested mappings' }))
    const dialog = await screen.findByRole('dialog', { name: 'Reset auto-mapping?' })
    expect(dialog).toHaveTextContent('Your manual mappings are kept.')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Reset' }))

    expect(await screen.findByRole('heading', { name: '4 of 7 target fields done' })).toBeInTheDocument()
    expect(screen.getByText('Source columns · auto-mapped 12 of 23 on the last run')).toBeInTheDocument()
    expect(calls).toEqual([{ ifMatch: '"3"', body: { mode: 'reset' } }])
    expect(await screen.findByRole('combobox', { name: 'Source for age' })).toHaveValue('age_text')
    expect(screen.getByRole('combobox', { name: 'Source for createdAt' })).toHaveValue('created_on')
  })
})
