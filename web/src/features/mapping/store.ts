import { followDesign } from '@/features/computed/refs'
import { createContext, useContext } from 'react'
import { createStore, useStore, type StoreApi } from 'zustand'
import { initHistory, push, redo, revert, squash, undo, type History } from '@/features/docschema/history'
import { parentMap } from './buildEdges'
import { computeStats } from './computeStats'
import type { Filter } from './selectVisible'
import type { MappingIssue, MappingSpec, MappingStats, MappingView, SourceColumn } from './types'
import type { Review } from '@/features/composer/review'

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
  /** The field last asked to be shown in the Canvas (OC-176); seq tells repeated asks apart. */
  reveal: { id: string; seq: number } | null
  conflict: boolean
  /** A proposal under review in the composer (OC-168); null when none. */
  review: Review | null
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
  /** Folds the steps since `mark` (history.past.length then) into one undo step. */
  squash: (mark: number) => void
  /** Drops the steps since `mark`, back to the state then. */
  revert: (mark: number) => void
  saved: (view: MappingView) => void
  replace: (view: MappingView) => void
  setConflict: (c: boolean) => void
  setReview: (r: Review | null) => void
  select: (s: Selection) => void
  setFilter: (f: Filter) => void
  setSearch: (q: string) => void
  setMode: (m: WorkbenchMode) => void
  toggleCollapsed: (fieldId: string) => void
  /**
   * Shows a field in the Canvas target pane (OC-176): selects it (or the given mapping), opens
   * its collapsed parents, clears a filter, and leaves the Table or Design tab for the Canvas.
   */
  revealField: (fieldId: string, selection?: Selection) => void
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
    reveal: null,
    conflict: false,
    review: null,
    revision: 0,
    flush: async () => {},
    spec: () => get().history.present,
    stats: () => computeStats(get().history.present, get().columns.map((c) => c.name)),
    apply: (fn) => {
      const cur = get().history.present
      // Computed fields follow renames and moves in the same undo step (OC-139).
      const next = followDesign(cur, fn(cur))
      if (next === cur) return
      set((s) => ({ history: push(s.history, next), revision: s.revision + 1 }))
    },
    undo: () => set((s) => ({ history: undo(s.history), revision: s.revision + 1 })),
    redo: () => set((s) => ({ history: redo(s.history), revision: s.revision + 1 })),
    squash: (mark) => set((s) => ({ history: squash(s.history, mark) })),
    revert: (mark) => set((s) => ({ history: revert(s.history, mark), revision: s.revision + 1 })),
    // A save round-trip: keep local edits made meanwhile, take the new version and issues.
    saved: (v) => set({ view: v, version: v.version, issues: v.issues, conflict: false }),
    // Server-side changes (auto-map, design replace, reload): the server's spec wins.
    replace: (v) => set((s) => ({ view: v, version: v.version, issues: v.issues, history: push(s.history, v.spec), conflict: false })),
    setConflict: (conflict) => set({ conflict }),
    setReview: (review) => set({ review }),
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
    revealField: (id, selection) =>
      set((s) => {
        const parents = parentMap(s.history.present)
        const collapsed = new Set(s.collapsed)
        for (let p = parents.get(id); p; p = parents.get(p)) collapsed.delete(p)
        return {
          selection: selection ?? { kind: 'field', id },
          collapsed,
          filter: 'all',
          search: '',
          mode: s.mode === 'table' || s.mode === 'design' ? 'canvas' : s.mode,
          reveal: { id, seq: (s.reveal?.seq ?? 0) + 1 },
        }
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
