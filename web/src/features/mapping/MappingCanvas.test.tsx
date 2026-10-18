import { act, fireEvent, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, onTestFinished, vi } from 'vitest'
import { dragOnto } from '@/test/drag'
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

/** Drags a target field onto another row; clientY picks the spot (rows measure 600 px tall here). */
const dragField = (from: string, onto: string, clientY: number) => dragOnto(screen.getByTestId(`target-${from}`), screen.getByTestId(`target-${onto}`), clientY)

const edges = () => [...document.querySelectorAll('[data-testid^="edge-"]')]

describe('MappingCanvas', () => {
  it('AC1: renders edges and badges', () => {
    renderWorkbench(<MappingCanvas />)
    // Active and pending mappings draw edges; the suggestion is dashed via its state.
    expect(edges().map((e) => e.getAttribute('data-state')).sort()).toEqual(['auto', 'auto', 'manual', 'suggested'])
    // Lines carry no transform labels until hovered or selected: then a small ƒ names the steps.
    expect(document.querySelectorAll('[data-testid^="badge-"]')).toHaveLength(0)
    fireEvent.mouseEnter(screen.getByTestId('source-created_on'))
    const marks = [...document.querySelectorAll('[data-testid^="badge-"]')]
    expect(marks.map((b) => [b.textContent?.replace(/\s+/g, ''), b.querySelector('title')?.textContent])).toEqual([['Formatdatetimeƒ', 'Format datetime']])
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

  it('target fields drag to a new place, keeping their mappings', () => {
    const { store } = renderWorkbench(<MappingCanvas />)
    const spec = () => store.getState().history.present
    dragField('email', 'customerId', 0) // top edge: before
    expect(spec().docSchema.root.children!.map((f) => f.name)).toEqual(['email', 'customerId', 'name', 'createdAt', 'age', 'profile'])
    dragField('age', 'name', 300) // a container's middle: inside
    expect(spec().docSchema.root.children![2]!.children!.map((f) => f.name)).toEqual(['first', 'last', 'age'])
    expect(screen.getByTestId('target-name.age')).toBeInTheDocument()
    expect(spec().fieldMappings.find((m) => m.id === 'm_email')?.targetFieldId).toBe('f_email')
  })

  it('OC-081 AC3: add field', async () => {
    const { store } = renderWorkbench(<MappingCanvas />)
    const root = () => store.getState().history.present.docSchema.root
    await userEvent.click(screen.getByRole('button', { name: 'Add field' }))
    await userEvent.keyboard('loyalty{Enter}')
    expect(root().children!.at(-1)!.name).toBe('loyalty')
    expect(store.getState().selection).toEqual({ kind: 'field', id: root().children!.at(-1)!.id })
    // A selected object takes the next field inside; Escape keeps the default name.
    await userEvent.click(screen.getByTestId('target-name'))
    await userEvent.click(screen.getByRole('button', { name: 'Add field to name' }))
    await userEvent.keyboard('{Escape}')
    expect(root().children![1]!.children!.map((f) => f.name)).toEqual(['first', 'last', 'newField'])
  })

  it('OC-145: click, double-click or F2 renames a target field', async () => {
    const { store } = renderWorkbench(<MappingCanvas />)
    const names = () => store.getState().history.present.docSchema.root.children!.map((f) => f.name)
    const label = (path: string) => within(screen.getByTestId(`target-${path}`)).getByText(path)
    const renameBox = (n: string) => screen.queryByRole('textbox', { name: `Rename ${n}` })
    // AC1: the first click selects, a click on the selected field's name opens the rename box.
    await userEvent.click(label('email'))
    expect(renameBox('email')).toBeNull()
    expect(label('email')).toHaveAttribute('title', 'Click to rename (F2)')
    await userEvent.click(label('email'))
    await userEvent.keyboard('mail{Enter}')
    expect(names()).toContain('mail')
    // AC2 + AC3: double-click opens it; Escape keeps the old name.
    await userEvent.dblClick(screen.getByTestId('target-age'))
    await userEvent.keyboard('years{Escape}')
    expect(names()).toContain('age')
    // F2 on the selected field; surrounding spaces are trimmed, a blank name keeps the old one.
    fireEvent.keyDown(screen.getByRole('application'), { key: 'F2' })
    await userEvent.keyboard('  years  {Enter}')
    expect(names()).toContain('years')
    fireEvent.keyDown(screen.getByRole('application'), { key: 'F2' })
    await userEvent.keyboard('{Backspace}{Enter}')
    expect(names()).toContain('years')
  })
  describe('OC-174: selecting one end reveals the other', () => {
    /** Records each pane's scrollTo calls (jsdom doesn't scroll). */
    function recordScrolls() {
      const scrolls: { el: Element; top: number }[] = []
      const original = Element.prototype.scrollTo
      onTestFinished(() => {
        Element.prototype.scrollTo = original
      })
      Element.prototype.scrollTo = vi.fn(function (this: Element, opts?: ScrollToOptions | number) {
        if (typeof opts === 'object' && opts.top != null) scrolls.push({ el: this, top: opts.top })
      }) as typeof Element.prototype.scrollTo
      vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(5000)
      vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(600)
      return scrolls
    }
    const pane = (name: RegExp) => screen.getByRole('region', { name })
    /** sampleSpec with 60 fields ahead of a "deep" object holding f_far, filled by first_name too. */
    function farView() {
      const spec = sampleSpec()
      const filler = Array.from({ length: 60 }, (_, i) => ({ id: `f_x${i}`, name: `x${i}`, type: 'string' as const }))
      spec.docSchema.root.children = [...filler, ...spec.docSchema.root.children!, { id: 'f_deep', name: 'deep', type: 'object', children: [{ id: 'f_far', name: 'far', type: 'string' }] }]
      spec.fieldMappings.push({ id: 'm_far', targetFieldId: 'f_far', sources: [{ column: 'first_name' }], transforms: [], origin: 'manual', state: 'active' })
      return mappingView({ spec })
    }

    it('AC1: a source click scrolls to its field, opening collapsed parents, and cycles 1 of N', async () => {
      const scrolls = recordScrolls()
      const { store } = renderWorkbench(<MappingCanvas />, farView())
      act(() => store.getState().toggleCollapsed('f_deep'))
      const target = pane(/^Target/)
      fireEvent.click(screen.getByTestId('source-first_name'))
      // first_name fills name.first, then deep.far: the first comes first.
      expect(screen.getByRole('status')).toHaveTextContent('first_name · 1 of 2')
      await vi.waitFor(() => expect(scrolls.some((s) => s.el === target && s.top > 0)).toBe(true))
      scrolls.length = 0
      fireEvent.click(screen.getByTestId('source-first_name'))
      expect(screen.getByRole('status')).toHaveTextContent('first_name · 2 of 2')
      expect(store.getState().collapsed.has('f_deep')).toBe(false)
      await vi.waitFor(() => expect(scrolls.some((s) => s.el === target && s.top > 0)).toBe(true))
      // Around again.
      fireEvent.click(screen.getByTestId('source-first_name'))
      expect(screen.getByRole('status')).toHaveTextContent('1 of 2')
    })

    it('AC2: a target click scrolls to its column; a mapping click to both ends', async () => {
      const scrolls = recordScrolls()
      const many = Array.from({ length: 60 }, (_, i) => scol(`a_${i}`, 'string', { ordinal: i + 1 }))
      const spec = sampleSpec()
      spec.docSchema.root.children!.push({ id: 'f_late', name: 'late', type: 'string' })
      spec.fieldMappings.push({ id: 'm_late', targetFieldId: 'f_late', sources: [{ column: 'a_59' }], transforms: [], origin: 'manual', state: 'active' })
      const { store } = renderWorkbench(<MappingCanvas />, mappingView({ spec }), [...columns, ...many])
      // Table order, so a_59 sits at the bottom of the source pane.
      fireEvent.click(screen.getByRole('radio', { name: 'Table' }))
      const source = pane(/^Source:/)
      scrolls.length = 0
      fireEvent.click(screen.getByTestId('target-late'))
      expect(store.getState().selection).toEqual({ kind: 'field', id: 'f_late' })
      await vi.waitFor(() => expect(scrolls.some((s) => s.el === source && s.top > 0)).toBe(true))
    })

    it('AC3: an end hidden by the filter says so and offers to show it', async () => {
      const { store } = renderWorkbench(<MappingCanvas />, farView())
      // Manual: first_name shows (it fills deep.far by hand), name.first (auto-mapped) doesn't.
      act(() => store.getState().setFilter('manual'))
      fireEvent.click(screen.getByTestId('source-first_name'))
      const notice = screen.getByRole('status')
      expect(notice).toHaveTextContent('name.first is hidden by the filter.')
      fireEvent.click(within(notice).getByRole('button', { name: 'Show it' }))
      expect(store.getState().filter).toBe('all')
      // The notice goes; the column's "1 of 2" stays.
      expect(screen.getByRole('status')).toHaveTextContent('first_name · 1 of 2')
    })
  })

  it('OC-175 AC3: the source order toggle is remembered', () => {
    localStorage.removeItem('odb.canvas.sourceOrder')
    onTestFinished(() => localStorage.removeItem('odb.canvas.sourceOrder'))
    const first = renderWorkbench(<MappingCanvas />)
    expect(screen.getByRole('radio', { name: 'Document' })).toHaveAttribute('aria-checked', 'true')
    // Document order: mapped columns by field, the unmapped after "Not mapped".
    expect(screen.getByTestId('divider-root')).toHaveTextContent('Not mapped')
    fireEvent.click(screen.getByRole('radio', { name: 'Table' }))
    expect(screen.queryByTestId('divider-root')).toBeNull()
    first.unmount()
    renderWorkbench(<MappingCanvas />)
    expect(screen.getByRole('radio', { name: 'Table' })).toHaveAttribute('aria-checked', 'true')
  })
  it('OC-176 AC2: a field revealed from the Issues panel scrolls into view and flashes', async () => {
    const scrolled: number[] = []
    const original = Element.prototype.scrollTo
    onTestFinished(() => {
      Element.prototype.scrollTo = original
    })
    Element.prototype.scrollTo = vi.fn(function (this: Element, opts?: ScrollToOptions | number) {
      if (typeof opts === 'object' && opts.top != null) scrolled.push(opts.top)
    }) as typeof Element.prototype.scrollTo
    vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(5000)
    vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(600)
    const spec = sampleSpec()
    spec.docSchema.root.children = [...Array.from({ length: 60 }, (_, i) => ({ id: `f_x${i}`, name: `x${i}`, type: 'string' as const })), ...spec.docSchema.root.children!]
    const { store } = renderWorkbench(<MappingCanvas />, mappingView({ spec }))
    act(() => store.getState().revealField('f_age'))
    await vi.waitFor(() => expect(scrolled.some((top) => top > 0)).toBe(true))
    act(() => store.getState().revealField('f_x1'))
    expect(screen.getByTestId('target-x1')).toHaveClass('odb-reveal')
    expect(screen.getByTestId('target-x0')).not.toHaveClass('odb-reveal')
  })

  it('OC-177 AC2: an unused lookup shows only its header until opened', async () => {
    const view = embedView()
    // A lookup of the same orders table (its columns come from the same fixture), read by nothing.
    view.spec.relations.push({ id: 'rel_lk', parentId: null, kind: 'many-to-one', lookupOnly: true, table: { schema: 'shop', name: 'orders' }, join: [{ parent: 'customer_id', child: 'customer_id' }] })
    view.embedTables = { ...view.embedTables, rel_lk: view.embedTables!.emb_o! }
    const { store } = renderWorkbench(<MappingCanvas />, view)
    const header = await screen.findByTestId('embed-group-rel_lk')
    expect(header).toHaveTextContent('orders (values only)· not used')
    expect(screen.getByTestId('source-emb_o:total')).toBeInTheDocument()
    expect(screen.queryByTestId('source-rel_lk:total')).toBeNull()
    fireEvent.click(within(header).getByRole('button', { name: "Show orders's columns" }))
    expect(await screen.findByTestId('source-rel_lk:total')).toBeInTheDocument()
    fireEvent.click(within(header).getByRole('button', { name: "Hide orders's columns" }))
    expect(screen.queryByTestId('source-rel_lk:total')).toBeNull()
    // Once a field reads it, it's an ordinary group.
    act(() => store.getState().apply((s) => ({ ...s, fieldMappings: [...s.fieldMappings, { id: 'm_lk', targetFieldId: 'f_age', sources: [{ relation: 'rel_lk', column: 'total' }], transforms: [], origin: 'manual', state: 'active' }] })))
    expect(screen.getByTestId('embed-group-rel_lk')).not.toHaveTextContent('not used')
    expect(screen.getByTestId('source-rel_lk:total')).toBeInTheDocument()
  })

  it('OC-179 AC4: a field filled with the current date and time shows a clock badge and no line', () => {
    const spec = sampleSpec()
    spec.fieldMappings.push({ id: 'm_now', targetFieldId: 'f_age', sources: [], transforms: [{ op: 'now', args: { at: 'run', format: 'epoch_ms' } }], origin: 'manual', state: 'active' })
    renderWorkbench(<MappingCanvas />, mappingView({ spec }))
    const badge = screen.getByTestId('now-age')
    expect(badge).toHaveTextContent('now · epoch ms')
    expect(badge).toHaveAttribute('title', 'the current date and time (when the run started, epoch ms)')
    expect(edges().some((e) => e.getAttribute('data-testid')!.startsWith('edge-m_now'))).toBe(false)
  })
})
