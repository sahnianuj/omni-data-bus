import { EyeOff, Plus } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { leaves } from '@/features/docschema/docschemaOps'
import { createFieldAndMap, ignoreColumn, OpError } from './mappingOps'
import { sourceStates, targetStates } from './selectVisible'
import { useWorkbench } from './store'
import type { MappingSpec, SourceColumn } from './types'

/** Unmapped source columns and target fields (OC-018 AC6). */
export function UnmappedPanel() {
  const { t } = useTranslation()
  const spec = useWorkbench((s) => s.history.present)
  const columns = useWorkbench((s) => s.columns)
  const apply = useWorkbench((s) => s.apply)
  const select = useWorkbench((s) => s.select)
  const ss = useMemo(() => sourceStates(spec, columns), [spec, columns])
  const ls = useMemo(() => leaves(spec.docSchema), [spec.docSchema])
  const ts = useMemo(() => targetStates(spec, ls), [spec, ls])
  const cols = columns.filter((c) => ss.get(c.name) === 'unmapped')
  const targets = ls.filter((l) => ts.get(l.fieldId) === 'unmapped').sort((a, b) => Number(b.required) - Number(a.required))

  const create = (s: MappingSpec, c: SourceColumn) => {
    try {
      return createFieldAndMap(s, c, s.docSchema.root.id).spec
    } catch (e) {
      if (e instanceof OpError) toast.error(e.message)
      return s
    }
  }

  return (
    <div className="flex flex-col gap-4 py-3">
      <section aria-label={t('mapping.unmappedSources')} className="flex flex-col gap-1.5">
        <div className="flex items-center justify-between px-4">
          <h3 className="text-xs font-medium uppercase tracking-wide text-subtle">{t('mapping.unmappedSources')}</h3>
          {cols.length > 1 ? (
            <Button size="sm" variant="link" className="h-6 px-0" onClick={() => apply((s) => cols.reduce(create, s))}>
              {t('mapping.createAll')}
            </Button>
          ) : null}
        </div>
        {cols.length === 0 ? <p className="px-4 text-xs text-muted">{t('mapping.noneUnmapped')}</p> : null}
        <ul>
          {cols.map((c) => (
            <li key={c.name} className="flex items-center gap-2 border-t border-border px-4 py-2 text-[13px]">
              <span className="font-mono text-xs">{c.name}</span>
              <span className="text-[11px] text-subtle">{c.dataType}</span>
              <span className="ml-auto flex gap-1">
                <Button size="icon" variant="ghost" className="size-7" aria-label={t('mapping.createFieldFor', { column: c.name })} onClick={() => apply((s) => create(s, c))}>
                  <Plus aria-hidden />
                </Button>
                <Button size="icon" variant="ghost" className="size-7" aria-label={t('mapping.ignoreColumn', { column: c.name })} onClick={() => apply((s) => ignoreColumn(s, c.name))}>
                  <EyeOff aria-hidden />
                </Button>
              </span>
            </li>
          ))}
        </ul>
      </section>
      <section aria-label={t('mapping.unmappedTargets')} className="flex flex-col gap-1.5">
        <h3 className="px-4 text-xs font-medium uppercase tracking-wide text-subtle">{t('mapping.unmappedTargets')}</h3>
        {targets.length === 0 ? <p className="px-4 text-xs text-muted">{t('mapping.noneUnmapped')}</p> : null}
        <ul>
          {targets.map((l) => (
            <li key={l.fieldId} className="border-t border-border">
              <button type="button" className="flex w-full items-center gap-2 px-4 py-2 text-left text-[13px] hover:bg-surface-muted" onClick={() => select({ kind: 'field', id: l.fieldId })}>
                <span className="font-mono text-xs">{l.path}</span>
                {l.required ? <span className="text-danger">{t('mapping.requiredMark')}</span> : null}
                <span className="ml-auto text-[11px] text-subtle">{l.type}</span>
              </button>
            </li>
          ))}
        </ul>
      </section>
    </div>
  )
}
