import { AlertTriangle, CheckCircle2, ChevronDown, ChevronRight, Lightbulb, OctagonAlert } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { pathsById } from '@/features/docschema/docschemaOps'
import { useWorkbench } from '@/features/mapping/store'
import { addToDesign, applyFix, removeBranch } from '@/features/mapping/fixes'
import { applyConstantHint, dismissConstantHint, repointSource } from './composerOps'

/**
 * The problems panel (OC-049): every validation issue with the node it's on; errors block
 * migrations and exports, warnings don't. Clicking one selects its node in the tree. Values of
 * an imported design that look fixed are offered as constants here too (OC-059).
 */
export function ProblemsPanel({ onCombineRows }: { onCombineRows?: (columns: string[]) => void } = {}) {
  const { t } = useTranslation()
  const issues = useWorkbench((s) => s.issues)
  const spec = useWorkbench((s) => s.history.present)
  const select = useWorkbench((s) => s.select)
  const apply = useWorkbench((s) => s.apply)
  const paths = useMemo(() => pathsById(spec.docSchema), [spec.docSchema])
  const byPath = useMemo(() => new Map([...paths].map(([id, p]) => [p, id])), [paths])
  const list = useMemo(() => issues.filter((i) => i.severity !== 'info').sort((a, b) => (a.severity === b.severity ? 0 : a.severity === 'error' ? -1 : 1)), [issues])
  // Hints on fields that still exist and aren't filled yet.
  const hints = useMemo(() => {
    const filled = new Set(spec.fieldMappings.filter((m) => m.state === 'active').map((m) => m.targetFieldId))
    return (spec.docSchema.constantHints ?? []).filter((h) => paths.has(h.fieldId) && !filled.has(h.fieldId))
  }, [spec.docSchema.constantHints, spec.fieldMappings, paths])
  // One line when collapsed: the tree's height comes first.
  const [open, setOpen] = useState(true)
  const extra = list.filter((i) => i.fix?.kind === 'add-to-design' && i.fieldId)
  const nodeOf = (i: (typeof issues)[number]) => i.fieldId ?? (i.nodePath ? byPath.get(i.nodePath) : undefined) ?? byPath.get(i.path)

  const hintList = hints.length ? (
    <section aria-label={t('composer.hints.title', { count: hints.length })} className="flex flex-col">
      <h3 className="px-3 pt-2 text-xs font-semibold uppercase tracking-wide text-muted">{t('composer.hints.title', { count: hints.length })}</h3>
      <ul className="flex flex-col py-1">
        {hints.map((h) => {
          const path = paths.get(h.fieldId)!
          const value = JSON.stringify(h.value)
          return (
            <li key={h.fieldId} className="flex items-start gap-2 px-3 py-1.5 text-xs">
              <Lightbulb className="mt-0.5 size-3.5 shrink-0 text-primary" aria-hidden />
              <button type="button" onClick={() => select({ kind: 'field', id: h.fieldId })} className="flex min-w-0 flex-1 flex-col text-left">
                <span className="font-mono text-[11px] text-muted">
                  {path} · {t(`composer.hints.reason.${h.reason}`)}
                </span>
                <span>{t('composer.hints.body', { path, value })}</span>
              </button>
              <Button size="sm" variant="secondary" aria-label={t('composer.hints.applyFor', { path, value })} onClick={() => apply((s) => applyConstantHint(s, h.fieldId))}>
                {t('composer.hints.apply')}
              </Button>
              <Button size="sm" variant="ghost" aria-label={t('composer.hints.dismissFor', { path })} onClick={() => apply((s) => dismissConstantHint(s, h.fieldId))}>
                {t('composer.hints.dismiss')}
              </Button>
            </li>
          )
        })}
      </ul>
    </section>
  ) : null

  if (list.length === 0) {
    return (
      <>
        {hintList}
        <p role="status" className="flex items-center gap-1.5 px-3 py-2 text-xs text-muted">
          <CheckCircle2 className="size-3.5 text-success" aria-hidden />
          {t('composer.problems.none')}
        </p>
      </>
    )
  }
  return (
    <>
      {hintList}
      <section aria-label={t('composer.problems.title', { count: list.length })} className="flex flex-col">
        <h3 className="flex items-center px-1.5 pt-1">
          <button type="button" aria-expanded={open} onClick={() => setOpen((o) => !o)} className="flex items-center gap-1 rounded px-1.5 py-0.5 text-xs font-semibold uppercase tracking-wide text-muted hover:bg-surface-muted">
            {open ? <ChevronDown className="size-3.5" aria-hidden /> : <ChevronRight className="size-3.5" aria-hidden />}
            {t('composer.problems.title', { count: list.length })}
          </button>
          {extra.length > 1 ? (
            <Button size="sm" variant="secondary" className="ml-2" onClick={() => apply((s) => extra.reduce((acc, i) => addToDesign(acc, i.fieldId!), s))}>
              {t('validation.addAllToDesign', { count: extra.length })}
            </Button>
          ) : null}
        </h3>
        <ul hidden={!open} className="flex flex-col py-1">
          {list.map((i, n) => {
            const id = nodeOf(i)
            const Icon = i.severity === 'error' ? OctagonAlert : AlertTriangle
            return (
              <li key={`${i.code}:${i.path}:${n}`}>
                <div className="flex items-start">
                <button
                  type="button"
                  disabled={!id}
                  onClick={() => id && select({ kind: 'field', id })}
                  className="flex min-w-0 flex-1 items-start gap-2 px-3 py-1.5 text-left text-xs hover:bg-surface-muted disabled:cursor-default"
                >
                  <Icon className={i.severity === 'error' ? 'mt-0.5 size-3.5 shrink-0 text-danger' : 'mt-0.5 size-3.5 shrink-0 text-warning'} aria-label={t(`composer.problems.${i.severity}`)} />
                  <span className="flex min-w-0 flex-col">
                    <span className="font-mono text-[11px] text-muted">
                      {i.nodePath || i.path} · {i.code}
                    </span>
                    <span>{i.message}</span>
                  </span>
                </button>
                {i.fix?.kind === 'combine-rows' && onCombineRows ? (
                  <Button size="sm" variant="secondary" className="m-1 shrink-0" onClick={() => onCombineRows(i.fix!.columns ?? [])}>
                    {i.fix.label}
                  </Button>
                ) : null}
                {i.fix?.kind === 'add-to-design' && i.fieldId ? (
                  <>
                    <Button size="sm" variant="secondary" className="my-1 ml-1 shrink-0" aria-label={`${i.fix.label}: ${i.nodePath || i.path}`} onClick={() => apply((s) => addToDesign(s, i.fieldId!))}>
                      {i.fix.label}
                    </Button>
                    <Button size="sm" variant="ghost" className="m-1 shrink-0" aria-label={t('validation.removeFor', { path: i.nodePath || i.path })} onClick={() => apply((s) => removeBranch(s, i.fieldId!))}>
                      {t('validation.remove')}
                    </Button>
                  </>
                ) : null}
                {i.fix?.kind === 'restore-field' || i.fix?.kind === 'map-field' ? (
                  <Button size="sm" variant="secondary" className="m-1 shrink-0" onClick={() => apply((s) => applyFix(s, i))}>
                    {i.fix.label}
                  </Button>
                ) : null}
                {i.fix?.kind === 'repoint-source' && i.fix.relationId && i.fieldMappingId ? (
                  <Button size="sm" variant="secondary" className="m-1 shrink-0" aria-label={`${i.fix.label}: ${i.nodePath || i.path}`} onClick={() => apply((s) => repointSource(s, i.fieldMappingId!, i.fix!.relationId!))}>
                    {i.fix.label}
                  </Button>
                ) : null}
                </div>
              </li>
            )
          })}
        </ul>
      </section>
    </>
  )
}
