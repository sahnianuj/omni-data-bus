import { Minus, Pencil, Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import type { SchemaDiff } from './api'

/** "View changes": what the last refresh found (OC-011 AC6). */
export function DiffSheet({ diff, open, onOpenChange }: { diff: SchemaDiff | null; open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t } = useTranslation()
  if (!diff) return null
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="left-auto right-0 top-0 h-full max-w-md translate-x-0 translate-y-0 overflow-y-auto rounded-none">
        <DialogHeader>
          <DialogTitle>{t('explorer.changesTitle')}</DialogTitle>
          <DialogDescription>{t('explorer.changesBody')}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-5 text-[13px]">
          {diff.addedTables.length ? (
            <section aria-label={t('explorer.added')}>
              <h3 className="mb-1.5 text-xs font-medium uppercase tracking-wide text-subtle">{t('explorer.added')}</h3>
              <ul className="flex flex-col gap-1">
                {diff.addedTables.map((n) => (
                  <li key={n} className="flex items-center gap-2 font-mono text-xs">
                    <Plus className="size-3.5 text-success" aria-hidden />
                    {n}
                  </li>
                ))}
              </ul>
            </section>
          ) : null}
          {diff.removedTables.length ? (
            <section aria-label={t('explorer.removed')}>
              <h3 className="mb-1.5 text-xs font-medium uppercase tracking-wide text-subtle">{t('explorer.removed')}</h3>
              <ul className="flex flex-col gap-1">
                {diff.removedTables.map((n) => (
                  <li key={n} className="flex items-center gap-2 font-mono text-xs">
                    <Minus className="size-3.5 text-danger" aria-hidden />
                    {n}
                  </li>
                ))}
              </ul>
            </section>
          ) : null}
          {diff.changedTables.length ? (
            <section aria-label={t('explorer.changedTables')}>
              <h3 className="mb-1.5 text-xs font-medium uppercase tracking-wide text-subtle">{t('explorer.changedTables')}</h3>
              <ul className="flex flex-col gap-3">
                {diff.changedTables.map((c) => (
                  <li key={c.table} className="flex flex-col gap-1">
                    <span className="flex items-center gap-2 font-mono text-xs font-medium">
                      <Pencil className="size-3.5 text-warning" aria-hidden />
                      {c.table}
                    </span>
                    <ul className="ml-5 flex flex-col gap-0.5 text-xs text-muted">
                      {c.addedColumns.map((col) => (
                        <li key={`+${col}`}>{t('explorer.columnAdded', { column: col })}</li>
                      ))}
                      {c.removedColumns.map((col) => (
                        <li key={`-${col}`}>{t('explorer.columnRemoved', { column: col })}</li>
                      ))}
                      {c.typeChanged.map((tc) => (
                        <li key={`~${tc.column}`}>{t('explorer.columnType', { column: tc.column, from: tc.from, to: tc.to })}</li>
                      ))}
                    </ul>
                  </li>
                ))}
              </ul>
            </section>
          ) : null}
        </div>
      </DialogContent>
    </Dialog>
  )
}
