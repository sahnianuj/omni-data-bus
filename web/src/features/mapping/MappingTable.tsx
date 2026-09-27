import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { leaves, pathsById } from '@/features/docschema/docschemaOps'
import { canConnect } from './canConnect'
import { activeFor, connect, OpError, replaceSource, unmap } from './mappingOps'
import { selectVisible } from './selectVisible'
import { useWorkbench } from './store'
import type { MappingSpec, SourceColumn } from './types'

const stateTone = { auto: 'success', suggested: 'warning', manual: 'primary', unmapped: 'neutral' } as const

/** Sets (or clears) the single source of a field — the table view's edit, identical to the canvas's. */
export function setSource(spec: MappingSpec, fieldId: string, column: SourceColumn | undefined): MappingSpec {
  if (!column) return unmap(spec, fieldId)
  const existing = activeFor(spec, fieldId)
  if (existing) return replaceSource(spec, existing.id, column)
  const res = connect(spec, column, fieldId)
  return res.outcome === 'connected' ? res.spec : spec
}

/** The accessible table view of the mapping (OC-019 AC7). */
export function MappingTable() {
  const { t } = useTranslation()
  const spec = useWorkbench((s) => s.history.present)
  const columns = useWorkbench((s) => s.columns)
  const filter = useWorkbench((s) => s.filter)
  const search = useWorkbench((s) => s.search)
  const issues = useWorkbench((s) => s.issues)
  const apply = useWorkbench((s) => s.apply)
  const select = useWorkbench((s) => s.select)
  const [tab, setTab] = useState('targets')
  const ls = useMemo(() => leaves(spec.docSchema), [spec.docSchema])
  const vis = useMemo(() => selectVisible(spec, columns, ls, filter, search, issues), [spec, columns, ls, filter, search, issues])
  const paths = useMemo(() => pathsById(spec.docSchema), [spec.docSchema])
  const rows = ls.filter((l) => vis.targets.includes(l.fieldId))
  const usable = columns.filter((c) => !spec.ignoredColumns.includes(c.name))

  const change = (fieldId: string, name: string) => {
    try {
      apply((s) => setSource(s, fieldId, columns.find((c) => c.name === name)))
    } catch (e) {
      if (e instanceof OpError) toast.error(e.message)
    }
  }

  return (
    <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col">
      <TabsList className="px-4">
        <TabsTrigger value="targets">{t('mapping.byTarget')}</TabsTrigger>
        <TabsTrigger value="sources">{t('mapping.bySource')}</TabsTrigger>
      </TabsList>
      <TabsContent value="targets" className="min-h-0 flex-1 overflow-auto">
        <table className="w-full border-collapse text-[13px]">
          <thead className="sticky top-0 bg-surface-muted text-left text-xs text-muted">
            <tr>
              {['target', 'type', 'source', 'transform', 'state', 'actions'].map((h) => (
                <th key={h} scope="col" className="border-b border-border px-3 py-2 font-medium">
                  {t(`mapping.col.${h}`)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((l) => {
              const m = activeFor(spec, l.fieldId) ?? spec.fieldMappings.find((x) => x.targetFieldId === l.fieldId && x.state === 'pending')
              const st = vis.targetStates.get(l.fieldId) ?? 'unmapped'
              return (
                <tr key={l.fieldId} className="border-b border-border">
                  <td className="px-3 py-1.5 font-mono text-xs">
                    {l.path}
                    {l.required ? <span className="text-danger"> {t('mapping.requiredMark')}</span> : null}
                  </td>
                  <td className="px-3 py-1.5 font-mono text-[11px] text-subtle">{l.itemType ? `array<${l.itemType}>` : l.type}</td>
                  <td className="px-3 py-1.5">
                    <select
                      aria-label={t('mapping.sourceFor', { path: l.path })}
                      value={m && m.state === 'active' && m.sources.length === 1 ? m.sources[0]!.column : ''}
                      onChange={(e) => change(l.fieldId, e.target.value)}
                      className="h-8 w-full rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 font-mono text-xs"
                    >
                      <option value="">{m && m.sources.length > 1 ? t('mapping.multiSource', { count: m.sources.length }) : t('mapping.noSource')}</option>
                      {usable.map((c) => (
                        <option key={c.name} value={c.name} disabled={!canConnect(c.jsonType, l.type).allowed}>
                          {c.name} ({c.jsonType})
                        </option>
                      ))}
                    </select>
                  </td>
                  <td className="px-3 py-1.5">
                    {m ? (
                      <Button variant="link" size="sm" className="h-7 px-0 font-mono text-xs" onClick={() => select({ kind: 'mapping', id: m.id })}>
                        {m.transforms.length ? m.transforms.map((s) => s.op).join(' → ') : t('mapping.noTransform')}
                      </Button>
                    ) : null}
                  </td>
                  <td className="px-3 py-1.5">
                    <Badge tone={stateTone[st]}>{t(`mapping.state.${st}`)}</Badge>
                  </td>
                  <td className="px-3 py-1.5">
                    {m ? (
                      <Button variant="destructive" size="sm" onClick={() => apply((s) => unmap(s, l.fieldId))}>
                        {t('mapping.unmap')}
                      </Button>
                    ) : null}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </TabsContent>
      <TabsContent value="sources" className="min-h-0 flex-1 overflow-auto">
        <table className="w-full border-collapse text-[13px]">
          <thead className="sticky top-0 bg-surface-muted text-left text-xs text-muted">
            <tr>
              <th scope="col" className="border-b border-border px-3 py-2 font-medium">{t('mapping.col.source')}</th>
              <th scope="col" className="border-b border-border px-3 py-2 font-medium">{t('mapping.col.targets')}</th>
            </tr>
          </thead>
          <tbody>
            {columns.map((c) => {
              const targets = spec.fieldMappings.filter((m) => m.state !== 'rejected' && m.sources.some((s) => s.column === c.name)).map((m) => paths.get(m.targetFieldId))
              return (
                <tr key={c.name} className="border-b border-border">
                  <td className="px-3 py-1.5 font-mono text-xs">{c.name}</td>
                  <td className="px-3 py-1.5 font-mono text-xs text-muted">
                    {spec.ignoredColumns.includes(c.name) ? t('mapping.ignored') : targets.length ? targets.join(', ') : '—'}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </TabsContent>
    </Tabs>
  )
}
