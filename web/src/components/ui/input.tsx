import { forwardRef, type InputHTMLAttributes } from 'react'
import { cn } from '@/lib/utils'

export type InputProps = InputHTMLAttributes<HTMLInputElement> & { invalid?: boolean }

export const Input = forwardRef<HTMLInputElement, InputProps>(({ className, invalid, ...props }, ref) => (
  <input
    ref={ref}
    aria-invalid={invalid || undefined}
    className={cn(
      'h-9 w-full rounded-[var(--radius-control)] border border-border-strong bg-surface px-3 text-sm text-foreground placeholder:text-subtle',
      'focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/30 disabled:opacity-50',
      invalid && 'border-danger-solid focus-visible:ring-danger-solid/30',
      className,
    )}
    {...props}
  />
))
Input.displayName = 'Input'
