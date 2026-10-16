import { AlertTriangle, ArrowRight, ArrowUpRight, Diamond, KeyRound, Lock } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { AsyncBoundary } from '@/components/AsyncBoundary'
import { Badge } from '@/components/ui/badge'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Tooltip } from '@/components/ui/tooltip'
import { formatBytes } from '@/lib/format'
import { cn } from '@/lib/utils'
import { usePreview, type SourceColumn, type SourceTable } from './api'
import { PreviewGrid } from './PreviewGrid'
import { relationshipGraph, type GraphNode } from './relationshipGraph'

type Nav = (schema: string, table: string) => void

function KeyGlyphs({ c, fk }: { c: SourceColumn; fk: boolean }) {
  const { t } = useTranslation()
  return (
    <span className="flex items-center gap-1 [&_svg]:size-3.5">
      {c.isPrimaryKey ? <KeyRound className="text-warning" aria-label={t('explorer.pk')} /> : null}
      {c.isUnique && !c.isPrimaryKey ? <Diamond className="text-muted" aria-label={t('explorer.unique')} /> : null}
      {fk ? <ArrowUpRight className="text-primary" aria-label={t('explorer.fk')} /> : null}
    </span>
  )
}

function ColumnsTab({ table }: { table: SourceTable }) {
  const { t } = useTranslation()
  const fkCols = new Set(table.foreignKeys.flatMap((f) => f.columns))
  return (
    <div className="overflow-auto rounded-[var(--radius-card)] border border-border">
      <table className="w-full border-collapse text-[13px]">
        <thead className="bg-surface-muted text-left text-xs text-muted">
          <tr>
            {['key', 'name', 'type', 'nullable', 'default', 'comment'].map((h) => (
              <th key={h} scope="col" className="border-b border-border px-3 py-2 font-medium">
                {t(`explorer.col.${h}`)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {table.columns.map((c) => (
            <tr key={c.name} className="border-b border-border last:border-0">
              <td className="w-14 px-3 py-2">
                <KeyGlyphs c={c} fk={fkCols.has(c.name)} />
              </td>
              <td className="px-3 py-2 font-medium">{c.name}</td>
              <td className="px-3 py-2">
                <span className="flex items-center gap-1.5">
                  <code className="font-mono text-xs">{c.dataType}</code>
                  <Badge tone="neutral" className="h-[18px] px-1.5 font-mono text-[11px]">
                    {c.isArray ? `${c.jsonType}<${c.elementType ?? ''}>` : c.jsonType}
                  </Badge>
                  {c.warning ? (
                    <Tooltip content={t('explorer.unsupported')}>
                      <AlertTriangle className="size-3.5 text-warning" aria-label={t('explorer.unsupported')} tabIndex={0} />
                    </Tooltip>
                  ) : null}
                </span>
              </td>
              <td className="px-3 py-2 text-muted">{c.nullable ? t('common.yes') : t('common.no')}</td>
              <td className="max-w-[200px] truncate px-3 py-2 font-mono text-xs text-muted">{c.default ?? ''}</td>
              <td className="max-w-[240px] truncate px-3 py-2 text-muted">{c.comment ?? ''}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function NodeCard({ node, onNavigate }: { node: GraphNode; onNavigate?: Nav }) {
  const { t } = useTranslation()
  const label = (
    <>
      <span className="text-xs text-subtle">{node.schema}.</span>
      <span className="font-medium">{node.table}</span>
    </>
  )
  if (!onNavigate) {
    return <div className="rounded-md border-2 border-primary bg-primary-soft px-3 py-2 text-[13px] text-primary-soft-foreground">{label}</div>
  }
  return (
    <button
      type="button"
      onClick={() => onNavigate(node.schema, node.table)}
      aria-label={t('explorer.openTable', { table: `${node.schema}.${node.table}` })}
      className="rounded-md border border-border bg-surface px-3 py-2 text-left text-[13px] hover:border-primary hover:bg-surface-muted"
    >
      {label}
    </button>
  )
}

function RelationshipsTab({ schema, table, onNavigate }: { schema: string; table: SourceTable; onNavigate: Nav }) {
  const { t } = useTranslation()
  const g = relationshipGraph(schema, table)
  if (g.edges.length === 0) return <p className="py-6 text-sm text-muted">{t('explorer.noRelationships')}</p>
  const column = (title: string, nodes: GraphNode[]) => (
    <div className="flex min-w-0 flex-1 flex-col gap-2">
      <h3 className="text-xs font-medium uppercase tracking-wide text-subtle">{title}</h3>
      {nodes.length ? nodes.map((n) => <NodeCard key={n.id} node={n} onNavigate={onNavigate} />) : <span className="text-xs text-subtle">—</span>}
    </div>
  )
  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-center gap-4 rounded-[var(--radius-card)] border border-border bg-background p-4" aria-label={t('explorer.diagram')}>
        {column(t('explorer.parents'), g.parents)}
        <ArrowRight className="size-4 shrink-0 rotate-180 text-subtle" aria-hidden />
        <div className="flex flex-1 justify-center">
          <NodeCard node={g.self} />
        </div>
        <ArrowRight className="size-4 shrink-0 rotate-180 text-subtle" aria-hidden />
        {column(t('explorer.children'), g.children)}
      </div>
      <ul className="flex flex-col gap-1.5 text-[13px]">
        {g.edges.map((e) => (
          <li key={`${e.from}-${e.name}`} className="font-mono text-xs text-muted">
            {t('explorer.edge', { name: e.name, from: e.from, columns: e.columns.join(', '), to: e.to, refColumns: e.refColumns.join(', ') })}
          </li>
        ))}
      </ul>
    </div>
  )
}

function IndexesTab({ table }: { table: SourceTable }) {
  const { t } = useTranslation()
  if (table.indexes.length === 0) return <p className="py-6 text-sm text-muted">{t('explorer.noIndexes')}</p>
  return (
    <ul className="flex flex-col divide-y divide-border rounded-[var(--radius-card)] border border-border">
      {table.indexes.map((ix) => (
        <li key={ix.name} className="flex items-center gap-3 px-3 py-2 text-[13px]">
          <span className="font-medium">{ix.name}</span>
          <code className="font-mono text-xs text-muted">({ix.columns.join(', ')})</code>
          <span className="ml-auto flex gap-1.5">
            {ix.primary ? <Badge tone="warning">{t('explorer.primary')}</Badge> : null}
            {ix.unique ? <Badge tone="primary">{t('explorer.uniqueIndex')}</Badge> : null}
          </span>
        </li>
      ))}
    </ul>
  )
}

const limits = [10, 50, 200] as const

function PreviewTab({ connId, schema, table }: { connId: string; schema: string; table: SourceTable }) {
  const { t } = useTranslation()
  const [limit, setLimit] = useState<number>(50)
  const preview = usePreview(connId, schema, table.name, limit, table.readable)
  if (!table.readable) {
    return (
      <div className="flex items-start gap-2.5 rounded-lg bg-warning-soft px-3.5 py-3 text-[13px] text-warning">
        <Lock className="mt-0.5 size-4 shrink-0" aria-hidden />
        {t('explorer.noAccessBody')}
      </div>
    )
  }
  return (
    <div className="flex flex-col gap-3">
      <label className="flex items-center gap-2 self-end text-xs text-muted">
        {t('explorer.rows')}
        <select
          value={limit}
          onChange={(e) => setLimit(Number(e.target.value))}
          className="h-8 rounded-[var(--radius-control)] border border-border-strong bg-surface px-2 text-[13px] text-foreground"
        >
          {limits.map((l) => (
            <option key={l} value={l}>
              {l}
            </option>
          ))}
        </select>
      </label>
      <AsyncBoundary query={preview}>{(r) => <PreviewGrid result={r} />}</AsyncBoundary>
    </div>
  )
}

/** The right-hand pane of the explorer: one table's structure, relationships, indexes and data. */
export function TableDetail({ connId, schema, table, onNavigate, action }: { connId: string; schema: string; table: SourceTable; onNavigate: Nav; action?: ReactNode }) {
  const { t } = useTranslation()
  const [tab, setTab] = useState('columns')
  const estimate =
    table.rowEstimate != null ? t(table.rowCountExact ? 'explorer.rowsExact' : 'explorer.rowsEstimate', { count: table.rowEstimate, formatted: table.rowEstimate.toLocaleString('en') }) : null
  return (
    <div className="flex min-w-0 flex-1 flex-col gap-4 overflow-y-auto px-6 py-5">
      <header className="flex flex-col gap-1.5">
        <div className="flex flex-wrap items-center gap-2.5">
          <h2 className="text-lg font-semibold">
            <span className="text-muted">{schema}.</span>
            {table.name}
          </h2>
          <Badge tone="neutral">{t(`explorer.kind.${table.kind}`)}</Badge>
          {!table.readable ? (
            <Badge tone="warning">
              <Lock className="size-3" aria-hidden />
              {t('explorer.noAccess')}
            </Badge>
          ) : null}
          {action ? <span className="ml-auto">{action}</span> : null}
        </div>
        <p className="text-[13px] text-muted">
          {estimate ?? (
            <Tooltip content={t('explorer.statsStale')}>
              <span tabIndex={0} className={cn(table.statsStale && 'cursor-help underline decoration-dotted')}>
                {table.kind.includes('view') ? t('explorer.noEstimateView') : '—'}
              </span>
            </Tooltip>
          )}
          {table.sizeBytes > 0 ? ` · ${formatBytes(table.sizeBytes)}` : ''}
          {table.partitions?.length ? ` · ${t('explorer.partitions', { count: table.partitions.length })}` : ''}
        </p>
        {table.comment ? <p className="text-[13px]">{table.comment}</p> : null}
      </header>
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="columns">{t('explorer.tab.columns', { count: table.columns.length })}</TabsTrigger>
          <TabsTrigger value="relationships">{t('explorer.tab.relationships')}</TabsTrigger>
          <TabsTrigger value="indexes">{t('explorer.tab.indexes')}</TabsTrigger>
          <TabsTrigger value="preview">{t('explorer.tab.preview')}</TabsTrigger>
        </TabsList>
        <TabsContent value="columns" className="pt-4">
          <ColumnsTab table={table} />
        </TabsContent>
        <TabsContent value="relationships" className="pt-4">
          <RelationshipsTab schema={schema} table={table} onNavigate={onNavigate} />
        </TabsContent>
        <TabsContent value="indexes" className="pt-4">
          <IndexesTab table={table} />
        </TabsContent>
        <TabsContent value="preview" className="pt-4">
          {tab === 'preview' ? <PreviewTab connId={connId} schema={schema} table={table} /> : null}
        </TabsContent>
      </Tabs>
    </div>
  )
}
