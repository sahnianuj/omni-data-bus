import { act, renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { createWorkbenchStore } from '@/features/mapping/store'
import { useMappingAutosave } from '@/features/mapping/useMappingAutosave'
import { composedView } from '@/test/composer'
import { mockMapping } from '@/test/mapping'
import { server, setupMswServer } from '@/test/msw'
import { addNode, bind, renameNode } from './composerOps'

setupMswServer()

// The composer edits the workbench store (OC-048 AC7): one undo history, 800 ms autosave with
// If-Match, and the 1.0 conflict handling.
describe('composer store', () => {
  let state: ReturnType<typeof mockMapping>
  beforeEach(() => {
    state = mockMapping(composedView())
  })

  it('AC7: undo history and autosave', async () => {
    const store = createWorkbenchStore(composedView(), [])
    const { result } = renderHook(() => useMappingAutosave(store, 'p1'))

    act(() => store.getState().apply((s) => renameNode(s, 'f_oid', 'id')))
    // Saved 800 ms after the edit, with the version it was based on.
    await new Promise((r) => setTimeout(r, 500))
    expect(state.puts).toHaveLength(0)
    await waitFor(() => expect(state.puts).toHaveLength(1), { timeout: 2000 })
    expect(state.puts[0]!.ifMatch).toBe('"3"')
    await waitFor(() => expect(result.current.state).toBe('saved'))

    // Ctrl+Z undoes the last edit, and that is saved too.
    act(() => store.getState().undo())
    expect(store.getState().history.present.docSchema.root.children![0]!.name).toBe('orderId')
    await waitFor(() => expect(state.puts).toHaveLength(2), { timeout: 2000 })
    expect(state.puts[1]!.ifMatch).toBe('"4"')
    expect(JSON.stringify(state.puts[1]!.body)).toContain('"name":"orderId"')

    // Binding edits are one undo step each, at least 50 steps deep.
    for (let i = 0; i < 55; i++) act(() => store.getState().apply((s) => addNode(s, 'fld_root', 'field', `f${i}`).spec))
    act(() => store.getState().apply((s) => bind(s, 'f_cust', { kind: 'many-to-one', table: { schema: 'public', name: 'customers' }, join: [{ parent: 'cust_code', child: 'code' }] }).spec))
    expect(store.getState().history.past.length).toBeGreaterThanOrEqual(50)
    act(() => store.getState().undo())
    expect(store.getState().history.present.relations.find((r) => r.id === 'rel_cust')!.join).toEqual([{ parent: 'customer_id', child: 'customer_id' }])
  })

  it('AC7: a 412 shows the 1.0 conflict prompt', async () => {
    server.use(http.put('*/api/v1/projects/:pid/mappings/:mid', () => HttpResponse.json({ title: 'Conflict', code: 'MAPPING_VERSION_CONFLICT', status: 412 }, { status: 412 })))
    const store = createWorkbenchStore(composedView(), [])
    const { result } = renderHook(() => useMappingAutosave(store, 'p1'))
    act(() => store.getState().apply((s) => renameNode(s, 'f_oid', 'id')))
    await waitFor(() => expect(result.current.state).toBe('conflict'), { timeout: 2000 })
    expect(store.getState().conflict).toBe(true)
  })
})
