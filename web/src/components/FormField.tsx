import type { ReactNode } from 'react'
import { Label } from './ui/label'

/** Label + control + hint/error, wired for screen readers via ids. */
export function FormField({ id, label, error, hint, children }: { id: string; label: ReactNode; error?: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children}
      {error ? (
        <p id={`${id}-error`} role="alert" className="text-xs text-danger">
          {error}
        </p>
      ) : hint ? (
        <p id={`${id}-hint`} className="text-xs text-subtle">
          {hint}
        </p>
      ) : null}
    </div>
  )
}
