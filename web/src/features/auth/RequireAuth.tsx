import type { ReactNode } from 'react'
import { Navigate, useLocation } from 'react-router'
import { ErrorState } from '@/components/ErrorState'
import { Skeleton } from '@/components/ui/skeleton'
import { useSession } from './api'

/** Renders children for signed-in users; otherwise redirects to /signin?next=<current path>. */
export function RequireAuth({ children }: { children: ReactNode }) {
  const session = useSession()
  const location = useLocation()
  if (session.status === 'pending') {
    return (
      <div role="status" aria-busy="true" className="flex h-screen items-center justify-center">
        <Skeleton className="h-8 w-48" />
      </div>
    )
  }
  if (session.status === 'error') {
    return (
      <div className="p-10">
        <ErrorState error={session.error} onRetry={() => void session.refetch()} />
      </div>
    )
  }
  if (!session.data) {
    const next = location.pathname + location.search
    return <Navigate to={`/signin?next=${encodeURIComponent(next)}`} replace />
  }
  return <>{children}</>
}
