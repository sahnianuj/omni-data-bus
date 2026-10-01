import { AlertCircle, AlertTriangle, Info } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { addToDesign, applyFix, groupIssues, issueTarget, removeBranch, severityOrder } from './fixes'
import { useWorkbench } from './store'
import type { MappingIssue } from './types'

const icons = { error: AlertCircle, warning: AlertTriangle, info: Info }
const tones = { error: 'text-danger', warning: 'text-warning', info: 'text-muted' }

/** Issues grouped by severity, each with *Go to* and an optional one-click fix (OC-022 AC2). */
export function ValidationPanel({ onEnableCreate, onCombineRows }: { onEnableCreate?: () => void; onCombineRows?: (columns: string[]) => void }) {
  const { t } = useTranslation()
  const issues = useWorkbench((s) => s.issues)
  const apply = useWorkbench((s) => s.apply)
  const select = useWorkbench((s) => s.select)
  const setMode = useWorkbench((s) => s.setMode)
  const groups = groupIssues(issues)

  if (!issues.length) return <p className="px-4 py-6 text-center text-[13px] text-muted">{t('validation.none')}</p>

  const fix = (i: MappingIssue) => {
    if (i.fix?.kind === 'enable-create') onEnableCreate?.()
    else if (i.fix?.kind === 'combine-rows') onCombineRows?.(i.fix.columns ?? [])
    else apply((s) => applyFix(s, i))
  }

  return (
    <div className="flex flex-col gap-4 px-4 py-3">
      {severityOrder.map((sev) => {
        const list = groups[sev]
        if (!list.length) return null
        const Icon = icons[sev]
        const extra = list.filter((i) => i.fix?.kind === 'add-to-design' && i.fieldId)
        return (
          <section key={sev} aria-label={t(`validation.${sev}`, { count: list.length })}>
            <h4 className={`mb-1.5 flex items-center gap-1.5 text-xs font-medium uppercase tracking-wide ${tones[sev]}`}>
              <Icon className="size-3.5" aria-hidden />
              {t(`validation.${sev}`, { count: list.length })}
              {extra.length > 1 ? (
                <Button size="sm" variant="secondary" className="ml-auto h-6 normal-case tracking-normal" onClick={() => apply((s) => extra.reduce((acc, i) => addToDesign(acc, i.fieldId!), s))}>
                  {t('validation.addAllToDesign', { count: extra.length })}
                </Button>
              ) : null}
            </h4>
            <ul className="flex flex-col gap-2">
              {list.map((i, n) => {
                const target = issueTarget(i)
                return (
                  <li key={`${i.code}-${i.path}-${n}`} className="rounded-md border border-border bg-surface p-2.5 text-[13px]">
                    <p>
                      {i.message}
                      {i.line ? <span className="text-subtle"> {t('validation.at', { line: i.line, column: i.column ?? 1 })}</span> : null}
                    </p>
                    <p className="mt-0.5 font-mono text-[11px] text-subtle">{i.code}</p>
                    <div className="mt-1.5 flex gap-2">
                      {target ? (
                        <Button
                          size="sm"
                          variant="link"
                          className="h-6 px-0"
                          onClick={() => {
                            if (target.kind === 'field' && !i.fieldMappingId) setMode('design')
                            select(target)
                          }}
                        >
                          {t('validation.goTo')}
                        </Button>
                      ) : null}
                      {i.fix && (i.fix.kind !== 'enable-create' || onEnableCreate) && (i.fix.kind !== 'combine-rows' || onCombineRows) ? (
                        <Button size="sm" variant="secondary" className="h-6" onClick={() => fix(i)}>
                          {i.fix.label}
                        </Button>
                      ) : null}
                      {i.fix?.kind === 'add-to-design' && i.fieldId ? (
                        <Button size="sm" variant="ghost" className="h-6" aria-label={t('validation.removeFor', { path: i.nodePath || i.path })} onClick={() => apply((s) => removeBranch(s, i.fieldId!))}>
                          {t('validation.remove')}
                        </Button>
                      ) : null}
                    </div>
                  </li>
                )
              })}
            </ul>
          </section>
        )
      })}
    </div>
  )
}
