import { useInfiniteQuery } from '@tanstack/react-query'
import { api, call } from '@/lib/api/client'
import type { components } from '@/lib/api/schema'

export type AuditEvent = components['schemas']['AuditEvent']

export type AuditFilter = { action?: string; userId?: string }

/** The workspace audit log (owners only, OC-033), newest first, 50 per page. */
export function useAuditLog(filter: AuditFilter, enabled = true) {
  return useInfiniteQuery({
    queryKey: ['audit', filter],
    enabled,
    initialPageParam: '',
    queryFn: ({ pageParam }) =>
      call(
        api.GET('/api/v1/audit', {
          params: { query: { limit: 50, cursor: pageParam || undefined, action: filter.action || undefined, userId: filter.userId || undefined } },
        }),
      ),
    getNextPageParam: (last) => last.nextCursor || undefined,
    retry: false,
  })
}
