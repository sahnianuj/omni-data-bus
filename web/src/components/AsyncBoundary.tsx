import type { UseQueryResult } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { ErrorState } from './ErrorState'
import { Skeleton } from './ui/skeleton'

type Props<T> = {
  query: Pick<UseQueryResult<T>, 'status' | 'data' | 'error' | 'refetch'>
  children: (data: T) => ReactNode
  isEmpty?: (data: T) => boolean
  empty?: ReactNode
  skeleton?: ReactNode
}

/** Renders the four standard async states: loading, error, empty and success (OC-004 AC4). */
export function AsyncBoundary<T>({ query, children, isEmpty, empty, skeleton }: Props<T>) {
  const { t } = useTranslation()
  if (query.status === 'pending') {
    return (
      <div role="status" aria-busy="true" aria-label={t('common.loading')}>
        {skeleton ?? (
          <div className="flex flex-col gap-3">
            <Skeleton className="h-6 w-1/3" />
            <Skeleton className="h-24 w-full" />
            <Skeleton className="h-24 w-full" />
          </div>
        )}
      </div>
    )
  }
  if (query.status === 'error') {
    return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  }
  const data = query.data as T
  if (isEmpty?.(data) && empty) return <>{empty}</>
  return <>{children(data)}</>
}
