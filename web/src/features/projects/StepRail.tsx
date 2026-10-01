import { Check, Lock } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { cn } from '@/lib/utils'
import type { Project } from './api'
import { deriveSteps, type StepId } from './steps'

export function StepRail({ project, current }: { project: Project; current: StepId }) {
  const { t } = useTranslation()
  const steps = deriveSteps(project, current)
  const anyLocked = steps.some((s) => s.state === 'locked')
  // The target's kind as configured (OC-036): Couchbase, or a directory of JSON files.
  const kind = project.target?.type
  const targetSub = kind === 'directory' ? t('wizard.targetSubDirectory') : kind ? t('wizard.targetSub') : t('wizard.targetSubAny')
  return (
    <nav aria-label={t('wizard.steps')} className="flex w-[264px] shrink-0 flex-col gap-1 border-r border-border bg-background px-5 py-7">
      {steps.map((s, i) => {
        const label = t(`wizard.${s.id}`)
        const marker =
          s.state === 'locked' ? (
            <Lock className="size-[15px] text-subtle" aria-hidden />
          ) : s.state === 'complete' ? (
            <span className="flex size-6 items-center justify-center rounded-full bg-success-dot text-white">
              <Check className="size-3.5" aria-hidden />
            </span>
          ) : (
            <span
              className={cn(
                'flex size-6 items-center justify-center rounded-full border-[1.5px] text-xs font-semibold',
                s.state === 'current' ? 'border-primary bg-primary text-primary-foreground' : 'border-border-strong text-muted',
                s.state === 'attention' && 'border-danger-solid text-danger',
              )}
            >
              {i + 1}
            </span>
          )
        const inner = (
          <>
            <span className="flex size-6 shrink-0 items-center justify-center">{marker}</span>
            <span className="flex flex-col gap-0.5">
              <span className={cn('text-sm', s.state === 'locked' ? 'text-subtle' : 'font-medium text-foreground')}>{label}</span>
              {s.id === 'source' || s.id === 'target' ? (
                <span className="text-xs text-subtle">{s.id === 'source' ? t('wizard.sourceSub') : targetSub}</span>
              ) : null}
            </span>
          </>
        )
        return (
          <div key={s.id}>
            {i === 2 ? <div className="my-2.5 h-px bg-border" /> : null}
            {s.state === 'locked' ? (
              <div aria-disabled="true" className="flex gap-3 rounded-lg p-2.5">
                {inner}
              </div>
            ) : (
              <Link
                to={`/projects/${project.id}/${s.id}`}
                aria-current={s.state === 'current' ? 'step' : undefined}
                data-state={s.state}
                className={cn('flex gap-3 rounded-lg border border-transparent p-2.5 hover:bg-surface', s.state === 'current' && 'border-primary-soft bg-surface shadow-sm')}
              >
                {inner}
              </Link>
            )}
          </div>
        )
      })}
      {anyLocked ? <p className="mx-2.5 mt-2 text-xs leading-[18px] text-subtle">{t('wizard.lockedHint')}</p> : null}
    </nav>
  )
}
