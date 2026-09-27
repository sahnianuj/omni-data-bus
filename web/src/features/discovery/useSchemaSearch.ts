import { useMemo, useState } from 'react'
import { buildIndex, flatten, search, type Chip, type SchemaNode } from './tree'

/**
 * Tree state for the schema explorer: fuzzy search over tables and columns, filter chips, and
 * expansion. Matches auto-expand their ancestors; nodes the user collapses during a search
 * stay collapsed until the query changes.
 */
export function useSchemaSearch(schemas: SchemaNode[]) {
  const [query, setQuery] = useState('')
  const [active, setActive] = useState<ReadonlySet<Chip>>(new Set())
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set())
  const [collapsed, setCollapsed] = useState<{ query: string; ids: ReadonlySet<string> }>({ query: '', ids: new Set() })

  const index = useMemo(() => buildIndex(schemas), [schemas])
  const result = useMemo(() => search(index, query), [index, query])

  const effective = useMemo(() => {
    const out = new Set(expanded)
    if (result) for (const id of result.expand) out.add(id)
    if (collapsed.query === query) for (const id of collapsed.ids) out.delete(id)
    return out
  }, [expanded, result, collapsed, query])

  const nodes = useMemo(() => flatten(schemas, effective, active, result), [schemas, effective, active, result])

  const setOpen = (id: string, open: boolean) => {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (open) next.add(id)
      else next.delete(id)
      return next
    })
    setCollapsed((prev) => {
      const ids = new Set(prev.query === query ? prev.ids : [])
      if (open) ids.delete(id)
      else ids.add(id)
      return { query, ids }
    })
  }

  const toggleChip = (chip: Chip) =>
    setActive((prev) => {
      const next = new Set(prev)
      if (next.has(chip)) next.delete(chip)
      else next.add(chip)
      return next
    })

  return { query, setQuery, active, toggleChip, expanded: effective, setOpen, nodes, highlights: result?.highlights ?? new Map<string, [number, number][]>() }
}
