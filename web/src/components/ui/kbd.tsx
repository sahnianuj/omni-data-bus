import type { HTMLAttributes } from 'react'
import { cn } from '@/lib/utils'

export function Kbd({ className, ...props }: HTMLAttributes<HTMLElement>) {
  return (
    <kbd
      className={cn('rounded border border-border bg-surface px-1.5 py-0.5 font-mono text-[11px] text-muted', className)}
      {...props}
    />
  )
}
