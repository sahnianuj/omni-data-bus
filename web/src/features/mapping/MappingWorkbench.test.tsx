import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { tbl } from '@/test/discovery'
import { withFixedLayout } from '@/test/layout'
import { columns, mappingView, mockMapping } from '@/test/mapping'
import { server, setupMswServer } from '@/test/msw'
import { makeProject, mockProject, repo } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'

setupMswServer()
withFixedLayout(320, 600)
beforeEach(() => {
  window.localStorage.clear()
  mockSession('owner')
  mockProject(makeProject({ source: repo('postgres'), target: repo('couchbase'), mappings: 1, readyMappings: 0 }))
  server.use(
    http.get('*/api/v1/connections/:id/schema/:schema/:table', () => HttpResponse.json(tbl('customer', { columns }))),
    http.get('*/api/v1/projects/:pid/mappings', () => HttpResponse.json({ items: [] })),
  )
})

const open = (view?: string) => renderRoutes(routes, `/projects/p1/mappings/m1${view ? `?view=${view}` : ''}`)
/** The field list (Table view, OC-067: opened on demand, not from the view bar). */
const openTable = () => open('table')

describe('MappingWorkbench', () => {
  it('opens without re-rendering forever', async () => {
    mockMapping()
    const canvas = open()
    expect(await screen.findByRole('heading', { name: /shop\.customer/ })).toBeInTheDocument()
    expect(screen.getByRole('application', { name: 'Mapping canvas' })).toBeInTheDocument()
    canvas.unmount()
    openTable()
    expect(await screen.findByRole('combobox', { name: 'Source for age' })).toBeInTheDocument()
  })

  it('a filter set from the score header shows in the toolbar, and Show all clears it', async () => {
    mockMapping()
    open()
    expect(await screen.findByRole('application', { name: 'Mapping canvas' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Show all' })).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'Show them' }))
    expect(await screen.findByText("Showing only what's not mapped")).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Show all' }))
    expect(screen.queryByText(/Showing only/)).toBeNull()
    // The required chip filters too, and the same way back.
    const required = screen.queryByRole('button', { name: /required fields? unmapped/ })
    if (required) {
      await userEvent.click(required)
      expect(await screen.findByText('Showing only required fields')).toBeInTheDocument()
      await userEvent.click(screen.getByRole('button', { name: 'Show all' }))
      expect(screen.queryByText(/Showing only/)).toBeNull()
    }
  })

  it('autosaves edits with If-Match on the current version', async () => {
    const state = mockMapping()
    openTable()
    await userEvent.selectOptions(await screen.findByRole('combobox', { name: 'Source for age' }), 'age_text')
    await waitFor(() => expect(state.puts).toHaveLength(1), { timeout: 3000 })
    expect(state.puts[0]!.ifMatch).toBe('"3"')
    expect(await screen.findByText('Saved · just now')).toBeInTheDocument()
  })

  it('a 412 shows the conflict banner; reload takes the server copy', async () => {
    const state = mockMapping()
    server.use(
      http.put('*/api/v1/projects/:pid/mappings/:mid', () =>
        HttpResponse.json({ title: 'Conflict', code: 'MAPPING_VERSION_CONFLICT', status: 412 }, { status: 412 }),
      ),
    )
    openTable()
    await userEvent.selectOptions(await screen.findByRole('combobox', { name: 'Source for age' }), 'age_text')
    expect(await screen.findByText(/Someone else changed this mapping/, {}, { timeout: 3000 })).toBeInTheDocument()
    state.view = mappingView({ version: 9 })
    await userEvent.click(screen.getByRole('button', { name: 'Reload' }))
    await waitFor(() => expect(screen.queryByText(/Someone else changed this mapping/)).not.toBeInTheDocument())
    expect(screen.getByRole('combobox', { name: 'Source for age' })).toHaveValue('')
  })

  it('the panels toggle open and closed, and validating opens them on Issues', async () => {
    mockMapping()
    server.use(http.post('*/api/v1/projects/:pid/mappings/:mid/validate', () => HttpResponse.json({ ready: false, issues: [] })))
    open()
    const toggle = await screen.findByRole('button', { name: 'Mapping panels' })
    expect(toggle).toHaveAttribute('aria-pressed', 'true')
    await userEvent.click(toggle)
    expect(screen.queryByRole('complementary', { name: 'Mapping panels' })).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Validate' }))
    expect(await screen.findByRole('complementary', { name: 'Mapping panels' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: /Issues/ })).toHaveAttribute('aria-selected', 'true')
  })
})
