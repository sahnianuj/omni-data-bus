import * as PopoverPrimitive from '@radix-ui/react-popover'
import type { ComponentPropsWithoutRef } from 'react'
import { cn } from '@/lib/utils'

export const Popover = PopoverPrimitive.Root
export const PopoverTrigger = PopoverPrimitive.Trigger
export const PopoverAnchor = PopoverPrimitive.Anchor

export function PopoverContent({ className, ...props }: ComponentPropsWithoutRef<typeof PopoverPrimitive.Content>) {
  return (
    <PopoverPrimitive.Portal>
      <PopoverPrimitive.Content
        sideOffset={6}
        className={cn('z-50 rounded-[var(--radius-card)] border border-border bg-surface p-4 shadow-lg outline-none', className)}
        {...props}
      />
    </PopoverPrimitive.Portal>
  )
}
