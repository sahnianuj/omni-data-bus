import type { FieldValues, Path, UseFormSetError } from 'react-hook-form'
import { ApiError } from '@/lib/api/client'

/** Copies server field errors onto the form; returns a general message for everything else. */
export function applyServerErrors<T extends FieldValues>(err: unknown, setError: UseFormSetError<T>, fields: Path<T>[]): string | null {
  if (!(err instanceof ApiError)) return err instanceof Error ? err.message : String(err)
  let mapped = false
  for (const fe of err.fieldErrors) {
    if ((fields as string[]).includes(fe.field)) {
      setError(fe.field as Path<T>, { type: 'server', message: fe.message.charAt(0).toUpperCase() + fe.message.slice(1) })
      mapped = true
    }
  }
  return mapped ? null : (err.detail ?? err.title)
}
