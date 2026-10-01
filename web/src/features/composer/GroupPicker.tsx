import { Rows3 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'

/**
 * "One document per" (OC-063): each row of the driving table, or each distinct value of some of
 * its columns — the customer's order_detail table grouped by order_id gives one order per
 * document, its lines in an array bound to the group's rows.
 */
export function GroupPicker({ table, columns, primaryKey, groupBy, onChange }: { table: string; columns: string[]; primaryKey: string[]; groupBy: string[]; onChange: (columns: string[]) => void }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [picked, setPicked] = useState<string[]>(groupBy)
  const [byColumns, setByColumns] = useState(groupBy.length > 0)
  const label = groupBy.length ? t('composer.group.distinct', { columns: groupBy.join(', ') }) : t('composer.group.row')
  return (
    <Popover
      open={open}
      onOpenChange={(o) => {
        setOpen(o)
        if (o) {
          setPicked(groupBy)
          setByColumns(groupBy.length > 0)
        }
      }}
    >
      <span className="flex items-center gap-2 text-xs font-medium text-muted">
        {t('composer.group.label')}
        <PopoverTrigger asChild>
          <Button size="sm" variant="secondary" aria-label={`${t('composer.group.label')}: ${label}`}>
            <Rows3 aria-hidden />
            {label}
          </Button>
        </PopoverTrigger>
      </span>
      <PopoverContent align="end" className="w-72 p-3 text-[13px]">
        <fieldset className="flex flex-col gap-2">
          <legend className="mb-1 text-xs font-semibold uppercase tracking-wide text-muted">{t('composer.group.title')}</legend>
          <label className="flex items-center gap-2">
            <input type="radio" name="group-mode" checked={!byColumns} onChange={() => setByColumns(false)} />
            {t('composer.group.each', { table })}
          </label>
          <label className="flex items-center gap-2">
            <input type="radio" name="group-mode" checked={byColumns} onChange={() => setByColumns(true)} />
            {t('composer.group.by')}
          </label>
          <ul aria-label={t('composer.group.by')} className="ml-6 flex max-h-48 flex-col gap-1 overflow-y-auto">
            {columns.map((c) => (
              <li key={c}>
                <label className="flex items-center gap-2 font-mono text-xs">
                  <input
                    type="checkbox"
                    disabled={!byColumns}
                    checked={picked.includes(c)}
                    onChange={(e) => setPicked((p) => (e.target.checked ? [...p, c] : p.filter((x) => x !== c)))}
                  />
                  {c}
                  {primaryKey.includes(c) ? <span className="text-muted">{t('composer.group.pk')}</span> : null}
                </label>
              </li>
            ))}
          </ul>
          <p className="text-xs text-muted">{t('composer.group.hint')}</p>
          <Button
            size="sm"
            className="self-end"
            disabled={byColumns && picked.length === 0}
            onClick={() => {
              // Keep the table's column order, whatever order they were ticked in.
              onChange(byColumns ? columns.filter((c) => picked.includes(c)) : [])
              setOpen(false)
            }}
          >
            {t('composer.group.apply')}
          </Button>
        </fieldset>
      </PopoverContent>
    </Popover>
  )
}
