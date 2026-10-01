import { createContext, useContext } from 'react'
import { createStore, useStore, type StoreApi } from 'zustand'
import { initHistory, push, redo, undo, type History } from '@/features/docschema/history'
import { computeStats } from './computeStats'
import type { Filter } from './selectVisible'
import type { MappingIssue, MappingSpec, MappingStats, MappingView, SourceColumn } from './types'

export type Selection = { kind: 'mapping'; id: string } | { kind: 'field'; id: string } | { kind: 'column'; name: string } | null
export type WorkbenchMode = 'canvas' | 'table' | 'design' | 'composer'

export type WorkbenchState = {
  view: MappingView // the last server response
  history: History<MappingSpec>
  columns: SourceColumn[]
  issues: MappingIssue[]
  version: number
  selection: Selection
  filter: Filter
  search: string
  mode: WorkbenchMode
  collapsed: ReadonlySet<string>
  conflict: boolean
  /** Increments on every local edit (autosave watches it). */
  revision: number
  /** Saves pending edits before a server-side change (set by the workbench's autosave). */
  flush: () => Promise<void>
  // derived
  spec: () => MappingSpec
  stats: () => MappingStats
  // actions
  apply: (fn: (spec: MappingSpec) => MappingSpec) => void
  undo: () => void
  redo: () => void
  saved: (view: MappingView) => void
  replace: (view: MappingView) => void
  setConflict: (c: boolean) => void
  select: (s: Selection) => void
  setFilter: (f: Filter) => void
  setSearch: (q: string) => void
  setMode: (m: WorkbenchMode) => void
  toggleCollapsed: (fieldId: string) => void
}

/** One store per open mapping (the workbench's single source of truth, with undo/redo). */
export function createWorkbenchStore(view: MappingView, columns: SourceColumn[]): StoreApi<WorkbenchState> {
  return createStore<WorkbenchState>((set, get) => ({
    view,
    history: initHistory(view.spec),
    columns,
    issues: view.issues,
    version: view.version,
    selection: null,
    filter: 'all',
    search: '',
    // A composed mapping opens in the composer (OC-048).
    mode: view.spec.mode === 'composed' ? 'composer' : 'canvas',
    collapsed: new Set(),
    conflict: false,
    revision: 0,
    flush: async () => {},
    spec: () => get().history.present,
    stats: () => computeStats(get().history.present, get().columns.map((c) => c.name)),
    apply: (fn) => {
      const cur = get().history.present
      const next = fn(cur)
      if (next === cur) return
      set((s) => ({ history: push(s.history, next), revision: s.revision + 1 }))
    },
    undo: () => set((s) => ({ history: undo(s.history), revision: s.revision + 1 })),
    redo: () => set((s) => ({ history: redo(s.history), revision: s.revision + 1 })),
    // A save round-trip: keep local edits made meanwhile, take the new version and issues.
    saved: (v) => set({ view: v, version: v.version, issues: v.issues, conflict: false }),
    // Server-side changes (auto-map, design replace, reload): the server's spec wins.
    replace: (v) => set((s) => ({ view: v, version: v.version, issues: v.issues, history: push(s.history, v.spec), conflict: false })),
    setConflict: (conflict) => set({ conflict }),
    select: (selection) => set({ selection }),
    setFilter: (filter) => set({ filter }),
    setSearch: (search) => set({ search }),
    setMode: (mode) => set({ mode }),
    toggleCollapsed: (id) =>
      set((s) => {
        const next = new Set(s.collapsed)
        if (next.has(id)) next.delete(id)
        else next.add(id)
        return { collapsed: next }
      }),
  }))
}

export const WorkbenchContext = createContext<StoreApi<WorkbenchState> | null>(null)

export function useWorkbench<T>(selector: (s: WorkbenchState) => T): T {
  const store = useContext(WorkbenchContext)
  if (!store) throw new Error('useWorkbench outside a workbench')
  return useStore(store, selector)
}

export function useWorkbenchStore(): StoreApi<WorkbenchState> {
  const store = useContext(WorkbenchContext)
  if (!store) throw new Error('useWorkbenchStore outside a workbench')
  return store
}
