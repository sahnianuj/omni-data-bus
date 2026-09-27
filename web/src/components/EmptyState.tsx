import type { ReactNode } from 'react'

export function EmptyState({ icon, title, body, action }: { icon?: ReactNode; title: ReactNode; body?: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 rounded-[var(--radius-card)] border border-dashed border-border-strong bg-surface px-6 py-16 text-center">
      {icon ? <div className="text-subtle [&_svg]:size-8">{icon}</div> : null}
      <h2 className="text-base font-semibold">{title}</h2>
      {body ? <p className="max-w-md text-sm text-muted">{body}</p> : null}
      {action ? <div className="mt-2">{action}</div> : null}
    </div>
  )
}
