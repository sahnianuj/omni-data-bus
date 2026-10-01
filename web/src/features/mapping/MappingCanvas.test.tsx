import { act, fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, onTestFinished, vi } from 'vitest'
import { withFixedLayout } from '@/test/layout'
import { columns, embedView, mappingView, renderWorkbench, sampleSpec, scol } from '@/test/mapping'
import { MappingCanvas } from './MappingCanvas'

withFixedLayout(320, 600)

/** A DataTransfer stand-in that remembers what drag start put into it. */
function dataTransfer() {
  const data = new Map<string, string>()
  return { setData: (k: string, v: string) => data.set(k, v), getData: (k: string) => data.get(k) ?? '', types: [] as string[], dropEffect: 'none', effectAllowed: 'all' }
}

function drag(column: string, onto: HTMLElement) {
  const dt = dataTransfer()
  fireEvent.dragStart(screen.getByTestId(`source-${column}`), { dataTransfer: dt })
  fireEvent.dragOver(onto, { dataTransfer: dt })
  fireEvent.drop(onto, { dataTransfer: dt })
}

const edges = () => [...document.querySelectorAll('[data-testid^="edge-"]')]

describe('MappingCanvas', () => {
  it('AC1: renders edges and badges', () => {
    renderWorkbench(<MappingCanvas />)
    // Active and pending mappings draw edges; the suggestion is dashed via its state.
    expect(edges().map((e) => e.getAttribute('data-state')).sort()).toEqual(['auto', 'auto', 'manual', 'suggested'])
    const badges = [...document.querySelectorAll('[data-testid^="badge-"]')].map((b) => b.textContent)
    expect(badges.sort()).toEqual(['ⓕ forma', 'ⓕ trim'])
    expect(screen.getByTestId('source-legacy_flag')).toHaveClass('line-through')
    expect(screen.getByText('Source: 8 columns')).toBeInTheDocument()
  })

  it('AC2: onConnect wires store and toasts lossy', async () => {
    const { store } = renderWorkbench(<MappingCanvas />)
    drag('age_text', screen.getByTestId('target-age'))
    const m = store.getState().history.present.fieldMappings.find((x) => x.targetFieldId === 'f_age')!
    expect(m).toMatchObject({ sources: [{ column: 'age_text' }], transforms: [{ op: 'parseInteger' }], state: 'active', needsReview: true })
    expect(store.getState().selection).toEqual({ kind: 'mapping', id: m.id })
    expect(await screen.findByText('Added a parseInteger step; check the converted values.')).toBeInTheDocument()
  })

  it('AC2: an occupied field asks to replace or add a source', async () => {
    const { store } = renderWorkbench(<MappingCanvas />)
    drag('last_name', screen.getByTestId('target-name.first'))
    await userEvent.click(await screen.findByRole('button', { name: 'Replace' }))
    const m = store.getState().history.present.fieldMappings.find((x) => x.id === 'm_first')!
    expect(m.sources).toEqual([{ column: 'last_name' }])
  })

  it('AC4: dropping on empty space creates a field and maps it', () => {
    const { store } = renderWorkbench(<MappingCanvas />)
    drag('loyalty_tier', screen.getByText('Drop a column here to create a field'))
    expect(store.getState().history.present.fieldMappings.some((m) => m.sources[0]?.column === 'loyalty_tier')).toBe(true)
  })

  it('AC6: hover adds highlight class to related edges', () => {
    renderWorkbench(<MappingCanvas />)
    fireEvent.mouseEnter(screen.getByTestId('source-first_name'))
    const related = edges().filter((e) => e.classList.contains('edge-related'))
    expect(related).toHaveLength(1)
    expect(edges().filter((e) => e.classList.contains('opacity-25'))).toHaveLength(3)
    fireEvent.mouseLeave(screen.getByTestId('source-first_name'))
    expect(edges().filter((e) => e.classList.contains('edge-related'))).toHaveLength(0)
  })

  it('deleting the selected edge disconnects it', () => {
    const { store } = renderWorkbench(<MappingCanvas />)
    act(() => store.getState().select({ kind: 'mapping', id: 'm_first' }))
    fireEvent.keyDown(screen.getByRole('application', { name: 'Mapping canvas' }), { key: 'Delete' })
    // Auto mappings are rejected (so re-runs don't bring them back); manual ones are removed.
    expect(store.getState().history.present.fieldMappings.find((m) => m.id === 'm_first')?.state).toBe('rejected')
    act(() => store.getState().select({ kind: 'mapping', id: 'm_created' }))
    fireEvent.keyDown(screen.getByRole('application', { name: 'Mapping canvas' }), { key: 'Delete' })
    expect(store.getState().history.present.fieldMappings.some((m) => m.id === 'm_created')).toBe(false)
  })

  it('AC10: row virtualization caps DOM rows', () => {
    const many = Array.from({ length: 500 }, (_, i) => scol(`col_${i}`, 'string', { ordinal: i + 1 }))
    renderWorkbench(<MappingCanvas />, mappingView({ spec: { ...sampleSpec(), fieldMappings: [] } }), [...columns, ...many])
    expect(screen.getByText('Source: 508 columns')).toBeInTheDocument()
    expect(document.querySelectorAll('[data-testid^="source-"]').length).toBeLessThan(60)
  })

  it('OC-021: embedded columns are a source group and map into the embed', () => {
    const { store } = renderWorkbench(<MappingCanvas />, embedView())
    expect(screen.getByTestId('embed-group-emb_o')).toHaveTextContent('orders → orders[] (array)')
    expect(screen.getByTestId('target-orders[].total')).toBeInTheDocument()
    expect(edges().some((e) => e.getAttribute('data-testid') === 'edge-m_total:emb_o:total')).toBe(true)
    drag('emb_o:order_date', screen.getByTestId('target-orders[].orderDate'))
    const m = store.getState().history.present.fieldMappings.find((x) => x.targetFieldId === 'f_date')!
    expect(m.sources).toEqual([{ column: 'order_date', relation: 'emb_o' }])
    // A parent column can't fill an embed field.
    drag('first_name', screen.getByTestId('target-orders[].total'))
    expect(store.getState().history.present.fieldMappings.filter((x) => x.targetFieldId === 'f_total')).toHaveLength(1)
  })

  it('colors each embed so its source columns and target fields are easy to spot', () => {
    renderWorkbench(<MappingCanvas />, embedView())
    const accent = (id: string) => screen.getByTestId(id).getAttribute('style') ?? ''
    expect(accent('embed-group-emb_o')).toContain('var(--embed-1)')
    expect(accent('source-emb_o:total')).toContain('var(--embed-1)')
    expect(accent('target-orders')).toContain('var(--embed-1)')
    expect(accent('target-orders[].total')).toContain('var(--embed-1)')
    expect(accent('source-first_name')).not.toContain('--embed')
    // The embed's mapping edges use a darker shade of the same color; others keep the state color.
    const stroke = (id: string) => screen.getByTestId(id).querySelectorAll('path')[1]!.getAttribute('stroke')
    expect(stroke('edge-m_total:emb_o:total')).toContain('var(--embed-1)')
    expect(edges().some((e) => !e.getAttribute('data-testid')!.includes('emb_') && e.querySelectorAll('path')[1]!.getAttribute('stroke')!.startsWith('var(--map-'))).toBe(true)
  })

  it('scrolls a newly added embed into view in both panes', async () => {
    const scrolled: number[] = []
    const original = Element.prototype.scrollTo
    onTestFinished(() => {
      Element.prototype.scrollTo = original
    })
    // jsdom has no scrolling; record where the virtualizer asks each pane to go.
    Element.prototype.scrollTo = vi.fn(function (this: Element, opts?: ScrollToOptions | number) {
      if (typeof opts === 'object' && opts.top != null) scrolled.push(opts.top)
    }) as typeof Element.prototype.scrollTo
    // The virtualizer clamps to scrollHeight - clientHeight, which jsdom reports as 0.
    vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(5000)
    vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(600)
    const many = Array.from({ length: 60 }, (_, i) => scol(`col_${i}`, 'string', { ordinal: i + 1 }))
    const view = embedView()
    view.spec.relations = []
    const { store } = renderWorkbench(<MappingCanvas />, view, [...columns, ...many])
    expect(scrolled.every((top) => top === 0)).toBe(true)
    act(() => store.getState().apply((s) => ({ ...s, relations: embedView().spec.relations })))
    await vi.waitFor(() => expect(scrolled.some((top) => top > 0)).toBe(true))
  })
})
