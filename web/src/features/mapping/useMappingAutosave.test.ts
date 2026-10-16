import { act, renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { columns, mappingView, mockMapping } from '@/test/mapping'
import { server, setupMswServer } from '@/test/msw'
import { createWorkbenchStore } from './store'
import type { MappingSpec } from './types'
import { useMappingAutosave } from './useMappingAutosave'

setupMswServer()

const ignoreAge = (s: MappingSpec): MappingSpec => ({ ...s, ignoredColumns: [...s.ignoredColumns, 'age_text'] })

describe('useMappingAutosave', () => {
  it('leaving the page saves a pending edit at once, and reports the save', async () => {
    const state = mockMapping()
    const store = createWorkbenchStore(mappingView(), columns)
    const saved: number[] = []
    const { unmount } = renderHook(() => useMappingAutosave(store, 'p1', 10_000, (v) => saved.push(v.version)))
    act(() => store.getState().apply(ignoreAge))
    unmount()
    await waitFor(() => expect(saved).toEqual([4]))
    expect(state.puts).toHaveLength(1)
    expect((state.puts[0]!.body as { spec: MappingSpec }).spec.ignoredColumns).toContain('age_text')
  })

  it('AC4: surfaces conflict and supports overwrite', async () => {
    const state = mockMapping()
    let conflicts = 1
    server.use(
      http.put('*/api/v1/projects/:pid/mappings/:mid', async ({ request }) => {
        const body = (await request.json()) as { spec: MappingSpec }
        state.puts.push({ ifMatch: request.headers.get('If-Match'), body })
        if (conflicts-- > 0) return HttpResponse.json({ title: 'Conflict', code: 'MAPPING_VERSION_CONFLICT', status: 412 }, { status: 412 })
        state.view = { ...state.view, spec: body.spec, version: state.view.version + 1 }
        return HttpResponse.json(state.view)
      }),
    )
    const store = createWorkbenchStore(mappingView(), columns)
    const { result } = renderHook(() => useMappingAutosave(store, 'p1', 10))

    act(() => store.getState().apply(ignoreAge))
    await waitFor(() => expect(result.current.state).toBe('conflict'))
    expect(store.getState().conflict).toBe(true)
    expect(state.puts.map((p) => p.ifMatch)).toEqual(['"3"'])

    // While in conflict, further edits don't autosave.
    act(() => store.getState().apply((s) => ({ ...s, nullHandling: 'omit' })))
    await new Promise((r) => setTimeout(r, 30))
    expect(state.puts).toHaveLength(1)

    // Another session moved the server to version 9; overwrite writes our spec on top of it.
    state.view = mappingView({ version: 9 })
    await act(() => result.current.overwrite())
    expect(state.puts).toHaveLength(2)
    expect(state.puts[1]!.ifMatch).toBe('"9"')
    const saved = (state.puts[1]!.body as { spec: MappingSpec }).spec
    expect(saved.ignoredColumns).toContain('age_text')
    expect(saved.nullHandling).toBe('omit')
    expect(result.current.state).toBe('saved')
    expect(store.getState().conflict).toBe(false)
    expect(store.getState().version).toBe(10)
    expect(result.current.dirty()).toBe(false)
  })
})
