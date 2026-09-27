import { Maximize2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import type { PreviewCell, PreviewResult } from './api'

const DISPLAY_CHARS = 1024

function text(v: unknown): string {
  return typeof v === 'string' ? v : JSON.stringify(v)
}

function JsonCell({ value }: { value: unknown }) {
  const json = JSON.stringify(value, null, 2)
  const summary = JSON.stringify(value)
  return (
    <details className="max-w-[360px]">
      <summary className="cursor-pointer truncate font-mono text-xs text-muted">{summary.length > 60 ? `${summary.slice(0, 60)}…` : summary}</summary>
      <pre className="mt-1 max-h-60 overflow-auto rounded bg-surface-muted p-2 font-mono text-xs">{json}</pre>
    </details>
  )
}

function Cell({ cell }: { cell: PreviewCell }) {
  const { t } = useTranslation()
  if (cell.v === null || cell.v === undefined) return <span className="italic text-subtle">{t('explorer.null')}</span>
  if (cell.t === 'any' && typeof cell.v === 'object') return <JsonCell value={cell.v} />
  const s = text(cell.v)
  if (!cell.truncated) return <span className="block max-w-[360px] truncate" title={s.length <= 200 ? s : undefined}>{s}</span>
  return (
    <span className="flex max-w-[360px] items-center gap-1">
      <span className="truncate">{s.slice(0, DISPLAY_CHARS)}…</span>
      <Popover>
        <PopoverTrigger asChild>
          <Button variant="ghost" size="icon" className="size-6 shrink-0" aria-label={t('explorer.expandValue')}>
            <Maximize2 className="size-3.5" aria-hidden />
          </Button>
        </PopoverTrigger>
        <PopoverContent className="w-[520px] max-w-[90vw]">
          <pre className="max-h-[420px] overflow-auto whitespace-pre-wrap break-all font-mono text-xs">{s}</pre>
        </PopoverContent>
      </Popover>
    </span>
  )
}

/** The Preview tab's grid: values exactly as a migration would encode them (OC-011 AC5). */
export function PreviewGrid({ result }: { result: PreviewResult }) {
  const { t } = useTranslation()
  if (result.rows.length === 0) return <p className="px-1 py-6 text-sm text-muted">{t('explorer.previewEmpty')}</p>
  return (
    <div className="overflow-auto rounded-[var(--radius-card)] border border-border">
      <table className="w-full border-collapse text-[13px]">
        <thead className="sticky top-0 bg-surface-muted">
          <tr>
            {result.columns.map((c) => (
              <th key={c.name} scope="col" className="whitespace-nowrap border-b border-border px-3 py-2 text-left font-medium">
                {c.name}
                <span className="ml-1.5 font-mono text-[11px] font-normal text-subtle">{c.jsonType}</span>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {result.rows.map((row, i) => (
            <tr key={i} className="border-b border-border last:border-0 hover:bg-surface-muted/50">
              {row.map((cell, j) => (
                <td key={result.columns[j]?.name ?? j} className="px-3 py-1.5 align-top font-mono text-xs">
                  <Cell cell={cell} />
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
