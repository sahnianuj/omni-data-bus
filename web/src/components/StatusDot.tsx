import { cn } from '@/lib/utils'

export type Status = 'ok' | 'warn' | 'failed' | 'untested' | 'running'

const color: Record<Status, string> = {
  ok: 'bg-success-dot',
  warn: 'bg-warning-dot',
  failed: 'bg-danger-solid',
  untested: 'bg-map-unmapped',
  running: 'bg-primary',
}

/** A colored dot; always pair it with a text label (color alone never carries meaning). */
export function StatusDot({ status, className }: { status: Status; className?: string }) {
  return <span aria-hidden className={cn('inline-block size-[7px] shrink-0 rounded-full', color[status], className)} />
}
