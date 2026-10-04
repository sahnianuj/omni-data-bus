import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import axe from 'axe-core'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { maximizeStore } from '@/lib/maximize'
import { composedSpec, mockComposer, mockOrdersMapping } from '@/test/composer'
import { mappingView, mockMapping } from '@/test/mapping'
import { server, setupMswServer } from '@/test/msw'
import { makeProject, mockProject, repo } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { mockSession } from '@/test/session'

setupMswServer()
beforeEach(() => {
  window.localStorage.clear()
  mockSession('owner')
  mockProject(makeProject({ source: repo('postgres'), target: repo('couchbase'), mappings: 1, readyMappings: 0 }))
  mockComposer()
  server.use(http.get('*/api/v1/projects/:pid/mappings', () => HttpResponse.json({ items: [] })))
})

const tab = (name: string) => screen.getByRole('tab', { name })

describe('ViewBar', () => {
  it('AC1: tabs with descriptions', async () => {
    mockOrdersMapping()
    renderRoutes(routes, '/projects/p1/mappings/m1')
    const bar = await screen.findByRole('tablist', { name: 'View' })
    // One line each (height is premium); what each is for is the tooltip and description.
    expect(within(bar).getAllByRole('tab').map((t) => t.getAttribute('aria-label'))).toEqual(['Canvas', 'Composer', 'Design'])
    expect(tab('Design')).toHaveAttribute('title', 'Fields, document key and JSON Schema. (Alt+3)')
    expect(tab('Composer')).toHaveAccessibleDescription('Design a nested document; fill arrays and objects from related tables or grouped rows.')
    // The first row under the page header, before the score header.
    const row = bar.parentElement!
    const header = row.parentElement!.querySelector('header')!
    expect(header.nextElementSibling).toBe(row)
    const res = await axe.run(bar, { rules: { 'color-contrast': { enabled: false } } })
    expect(res.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')).toEqual([])
  })

  it('AC2: remembered, deep link wins', async () => {
    mockOrdersMapping()
    const first = renderRoutes(routes, '/projects/p1/mappings/m1')
    await userEvent.click(await screen.findByRole('tab', { name: 'Design' }))
    expect(tab('Design')).toHaveAttribute('aria-selected', 'true')
    first.unmount()

    renderRoutes(routes, '/projects/p1/mappings/m1')
    await waitFor(() => expect(tab('Design')).toHaveAttribute('aria-selected', 'true'))
  })

  it('AC2: a link picks the view; a composed mapping opens in the Composer', async () => {
    mockOrdersMapping()
    window.localStorage.setItem('odb.view.m1', 'composer')
    const linked = renderRoutes(routes, '/projects/p1/mappings/m1?view=design')
    await waitFor(() => expect(tab('Design')).toHaveAttribute('aria-selected', 'true'))
    linked.unmount()

    mockMapping(mappingView({ sourceSchema: 'public', sourceTable: 'orders', spec: composedSpec(), embedCandidates: [], embedTables: {}, issues: [] }))
    renderRoutes(routes, '/projects/p1/mappings/m1')
    await waitFor(() => expect(tab('Composer')).toHaveAttribute('aria-selected', 'true'))
  })

  it('AC5: keyboard', async () => {
    mockOrdersMapping()
    renderRoutes(routes, '/projects/p1/mappings/m1')
    const canvas = await screen.findByRole('tab', { name: 'Canvas' })
    canvas.focus()
    await userEvent.keyboard('{ArrowRight}{ArrowRight}')
    expect(tab('Design')).toHaveAttribute('aria-selected', 'true')
    expect(tab('Design')).toHaveFocus()
    await userEvent.keyboard('{Home}')
    expect(tab('Canvas')).toHaveAttribute('aria-selected', 'true')
    fireEvent.keyDown(window, { key: '3', code: 'Digit3', altKey: true })
    await waitFor(() => expect(tab('Design')).toHaveAttribute('aria-selected', 'true'))
    fireEvent.keyDown(window, { key: '1', code: 'Digit1', altKey: true })
    await waitFor(() => expect(tab('Canvas')).toHaveAttribute('aria-selected', 'true'))
    fireEvent.keyDown(window, { key: '2', code: 'Digit2', altKey: true })
    await waitFor(() => expect(tab('Composer')).toHaveAttribute('aria-selected', 'true'))
  })

  it('AC4: composer hint', async () => {
    // A canvas mapping with an embedded array: the Canvas suggests the Composer, once.
    mockMapping(mappingView({ sourceSchema: 'public', sourceTable: 'orders', spec: { ...composedSpec(), mode: 'canvas', root: undefined }, embedCandidates: [], embedTables: {}, issues: [] }))
    const first = renderRoutes(routes, '/projects/p1/mappings/m1?view=canvas')
    const hint = await screen.findByText('This document has nested arrays or objects — the Composer is made for that.')
    await userEvent.click(within(hint.parentElement!).getByRole('button', { name: "Don't show again" }))
    expect(screen.queryByText(/the Composer is made for that/)).not.toBeInTheDocument()
    first.unmount()
    renderRoutes(routes, '/projects/p1/mappings/m1?view=canvas')
    await screen.findByRole('tab', { name: 'Canvas', selected: true })
    expect(screen.queryByText(/the Composer is made for that/)).not.toBeInTheDocument()
  })

  it('OC-067 AC2: table on demand', async () => {
    mockOrdersMapping()
    window.localStorage.setItem('odb.view.m1', 'table')
    const remembered = renderRoutes(routes, '/projects/p1/mappings/m1')
    await waitFor(() => expect(tab('Canvas')).toHaveAttribute('aria-selected', 'true')) // a remembered Table opens the Canvas
    remembered.unmount()

    renderRoutes(routes, '/projects/p1/mappings/m1?view=table')
    const back = await screen.findByRole('button', { name: 'Back to Canvas' })
    expect(screen.queryByRole('tab', { name: 'Table' })).not.toBeInTheDocument()
    expect(within(screen.getByRole('tablist', { name: 'View' })).getAllByRole('tab').every((t) => t.getAttribute('aria-selected') === 'false')).toBe(true)
    await userEvent.click(back)
    expect(tab('Canvas')).toHaveAttribute('aria-selected', 'true')
  })
  it('maximize height hides the page chrome while the workbench is open', async () => {
    mockOrdersMapping()
    const wb = renderRoutes(routes, '/projects/p1/mappings/m1')
    await userEvent.click(await screen.findByRole('button', { name: 'Maximize height' }))
    expect(screen.queryByRole('button', { name: /Search or jump to/ })).not.toBeInTheDocument()
    expect(screen.getByRole('navigation', { name: 'Project steps' })).toBeInTheDocument()
    expect(window.localStorage.getItem('oc-workbench-maximized')).toBe('true')
    await userEvent.click(screen.getByRole('button', { name: 'Restore height' }))
    expect(screen.getByRole('button', { name: /Search or jump to/ })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Maximize height' }))
    wb.unmount()

    // Other pages keep their chrome even while the choice is remembered.
    renderRoutes(routes, '/projects/p1/mappings')
    expect(await screen.findByRole('button', { name: /Search or jump to/ })).toBeInTheDocument()
    maximizeStore.setState({ maximized: false })
  })

  it('OC-083: ?panel=issues opens the Issues tab', async () => {
    mockOrdersMapping()
    renderRoutes(routes, '/projects/p1/mappings/m1?view=canvas&panel=issues')
    expect(await screen.findByRole('tab', { name: /^Issues/ })).toHaveAttribute('aria-selected', 'true')
  })
})
