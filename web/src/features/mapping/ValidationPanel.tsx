import { AlertCircle, AlertTriangle, Info } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { applyFix, groupIssues, issueTarget, severityOrder } from './fixes'
import { useWorkbench } from './store'
import type { MappingIssue } from './types'

const icons = { error: AlertCircle, warning: AlertTriangle, info: Info }
const tones = { error: 'text-danger', warning: 'text-warning', info: 'text-muted' }

/** Issues grouped by severity, each with *Go to* and an optional one-click fix (OC-022 AC2). */
export function ValidationPanel({ onEnableCreate }: { onEnableCreate?: () => void }) {
  const { t } = useTranslation()
  const issues = useWorkbench((s) => s.issues)
  const apply = useWorkbench((s) => s.apply)
  const select = useWorkbench((s) => s.select)
  const setMode = useWorkbench((s) => s.setMode)
  const groups = groupIssues(issues)

  if (!issues.length) return <p className="px-4 py-6 text-center text-[13px] text-muted">{t('validation.none')}</p>

  const fix = (i: MappingIssue) => {
    if (i.fix?.kind === 'enable-create') onEnableCreate?.()
    else apply((s) => applyFix(s, i))
  }

  return (
    <div className="flex flex-col gap-4 px-4 py-3">
      {severityOrder.map((sev) => {
        const list = groups[sev]
        if (!list.length) return null
        const Icon = icons[sev]
        return (
          <section key={sev} aria-label={t(`validation.${sev}`, { count: list.length })}>
            <h4 className={`mb-1.5 flex items-center gap-1.5 text-xs font-medium uppercase tracking-wide ${tones[sev]}`}>
              <Icon className="size-3.5" aria-hidden />
              {t(`validation.${sev}`, { count: list.length })}
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
                      {i.fix && (i.fix.kind !== 'enable-create' || onEnableCreate) ? (
                        <Button size="sm" variant="secondary" className="h-6" onClick={() => fix(i)}>
                          {i.fix.label}
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
