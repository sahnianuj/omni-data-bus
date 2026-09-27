import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { jobKeys, type ErrorSample, type Job, type ProgressSnapshot } from '@/features/jobs/api'
import { projectKeys } from '@/features/projects/api'

export type Connection = 'live' | 'reconnecting' | 'offline'

/** How long a hidden tab keeps its stream open before pausing it (OC-027). */
export const HIDDEN_PAUSE_MS = 5 * 60 * 1000

type Options = {
  enabled?: boolean
  /** Called for each error-sample event (the live error feed). */
  onErrorSample?: (s: ErrorSample) => void
  /** Called once when the job reaches a terminal state. */
  onCompleted?: (s: ProgressSnapshot) => void
}

/**
 * Streams a job's progress over SSE into the ['job', id] query cache, so every component reads
 * one source of truth (OC-027 AC7). EventSource reconnects by itself; the hook reports the
 * connection state, pauses while the tab has been hidden for 5 minutes, and on "completed"
 * closes the stream and refetches the job.
 */
export function useJobEvents(jobId: string, { enabled = true, onErrorSample, onCompleted }: Options = {}) {
  const qc = useQueryClient()
  const [connection, setConnection] = useState<Connection>('reconnecting')
  const [paused, setPaused] = useState(false)
  const callbacks = useRef({ onErrorSample, onCompleted })
  useEffect(() => {
    callbacks.current = { onErrorSample, onCompleted }
  })

  // Pause after the tab has been hidden a while; on return, reconnect and refetch.
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined
    const onVisibility = () => {
      if (document.hidden) {
        timer = setTimeout(() => setPaused(true), HIDDEN_PAUSE_MS)
      } else {
        clearTimeout(timer)
        setPaused((was) => {
          if (was) void qc.invalidateQueries({ queryKey: jobKeys.one(jobId) })
          return false
        })
      }
    }
    document.addEventListener('visibilitychange', onVisibility)
    return () => {
      clearTimeout(timer)
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [jobId, qc])

  useEffect(() => {
    if (!enabled || paused || !jobId) return
    const es = new EventSource(`/api/v1/jobs/${encodeURIComponent(jobId)}/events`, { withCredentials: true })
    let done = false
    const merge = (snap: ProgressSnapshot) =>
      qc.setQueryData<Job>(jobKeys.one(jobId), (job) => {
        if (!job) return job
        // Ignore anything older than what the cache has (reconnects replay the latest state).
        if (job.progress && snap.seq < job.progress.seq && snap.status === job.status) return job
        return { ...job, status: snap.status, progress: snap }
      })
    const parse = <T,>(e: MessageEvent) => {
      try {
        return JSON.parse(e.data as string) as T
      } catch {
        return null
      }
    }
    es.onopen = () => setConnection('live')
    es.onerror = () => {
      if (!done) setConnection(es.readyState === EventSource.CLOSED ? 'offline' : 'reconnecting')
    }
    es.addEventListener('progress', (e) => {
      setConnection('live')
      const s = parse<ProgressSnapshot>(e as MessageEvent)
      if (s) merge(s)
    })
    es.addEventListener('error-sample', (e) => {
      const s = parse<ErrorSample>(e as MessageEvent)
      if (s) callbacks.current.onErrorSample?.(s)
    })
    es.addEventListener('completed', (e) => {
      done = true
      const s = parse<ProgressSnapshot>(e as MessageEvent)
      if (s) merge(s)
      es.close()
      setConnection('offline')
      void qc.invalidateQueries({ queryKey: jobKeys.one(jobId) })
      void qc.invalidateQueries({ queryKey: jobKeys.all })
      void qc.invalidateQueries({ queryKey: projectKeys.all }) // the project's status follows its job
      if (s) callbacks.current.onCompleted?.(s)
    })
    return () => {
      done = true
      es.close()
    }
  }, [jobId, enabled, paused, qc])

  return { connection: !enabled || paused || !jobId ? ('offline' as const) : connection }
}
