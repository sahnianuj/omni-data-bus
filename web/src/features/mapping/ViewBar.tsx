import { Braces, FileJson2, Spline, Table2 } from 'lucide-react'
import { useRef, type KeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import type { WorkbenchMode } from './store'
import { BAR_VIEWS as VIEWS } from './viewPrefs'

const icons: Record<WorkbenchMode, typeof Spline> = { canvas: Spline, composer: Braces, table: Table2, design: FileJson2 }

/**
 * The workbench's four views as a row of clearly visible, single-line tab buttons (OC-062):
 * Canvas, Composer, Table, Design. What each is for is in its tooltip and accessible description
 * (height is premium: no second line). Arrow keys move between tabs (Home/End to the ends);
 * Alt+1…4 switch from anywhere in the workbench.
 */
export function ViewBar({ mode, onChange }: { mode: WorkbenchMode; onChange: (m: WorkbenchMode) => void }) {
  const { t } = useTranslation()
  const refs = useRef<(HTMLButtonElement | null)[]>([])
  const onKey = (e: KeyboardEvent, i: number) => {
    const next = e.key === 'ArrowRight' ? i + 1 : e.key === 'ArrowLeft' ? i - 1 : e.key === 'Home' ? 0 : e.key === 'End' ? VIEWS.length - 1 : null
    if (next === null) return
    e.preventDefault()
    const m = VIEWS[(next + VIEWS.length) % VIEWS.length]!
    onChange(m)
    refs.current[VIEWS.indexOf(m)]?.focus()
  }
  return (
    <div role="tablist" aria-label={t('workbench.view')} className="flex flex-wrap items-center gap-1.5 border-b border-border px-6 py-2">
      {VIEWS.map((m, i) => {
        const Icon = icons[m]
        const selected = mode === m
        return (
          <button
            key={m}
            ref={(el) => {
              refs.current[i] = el
            }}
            type="button"
            role="tab"
            id={`view-tab-${m}`}
            aria-selected={selected}
            aria-label={t(`workbench.mode.${m}`)}
            aria-describedby={`view-desc-${m}`}
            aria-keyshortcuts={`Alt+${i + 1}`}
            tabIndex={selected || (i === 0 && !VIEWS.includes(mode)) ? 0 : -1}
            title={`${t(`workbench.modeHint.${m}`)} (Alt+${i + 1})`}
            onClick={() => onChange(m)}
            onKeyDown={(e) => onKey(e, i)}
            className={cn(
              'flex h-8 items-center gap-1.5 rounded-[var(--radius-control)] border px-3 text-[13px] font-medium shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/60',
              selected ? 'border-primary bg-primary text-primary-foreground' : 'border-border-strong bg-surface text-foreground hover:bg-surface-muted',
            )}
          >
            <Icon className="size-4 shrink-0" aria-hidden />
            {t(`workbench.mode.${m}`)}
            <span id={`view-desc-${m}`} className="sr-only">
              {t(`workbench.modeHint.${m}`)}
            </span>
          </button>
        )
      })}
    </div>
  )
}
