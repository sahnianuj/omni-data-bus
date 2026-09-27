import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook } from '@testing-library/react'
import { createElement, type ReactNode } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { jobKeys, type Job } from '@/features/jobs/api'
import { FakeEventSource, withFakeEventSource } from '@/test/eventSource'
import { job, snapshot } from '@/test/jobs'
import { useJobEvents } from './useJobEvents'

withFakeEventSource()

function setup(opts: Parameters<typeof useJobEvents>[1] = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData(jobKeys.one('j1'), job({ progress: snapshot({ seq: 1, processed: 10 }) }))
  const wrapper = ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client: qc }, children)
  const hook = renderHook(() => useJobEvents('j1', opts), { wrapper })
  return { qc, hook, es: FakeEventSource.latest() }
}

describe('useJobEvents', () => {
  it('AC7: merges progress into cache', () => {
    const { qc, es } = setup()
    expect(es.url).toBe('/api/v1/jobs/j1/events')
    expect(es.init).toEqual({ withCredentials: true })
    act(() => es.emit('progress', snapshot({ seq: 2, processed: 500, percent: 12.5 })))
    const cached = qc.getQueryData<Job>(jobKeys.one('j1'))!
    expect(cached.progress.processed).toBe(500)
    expect(cached.progress.percent).toBe(12.5)
    // An older snapshot (a replay after reconnecting) is ignored.
    act(() => es.emit('progress', snapshot({ seq: 1, processed: 10 })))
    expect(qc.getQueryData<Job>(jobKeys.one('j1'))!.progress.processed).toBe(500)
    // A status change moves the job's status too.
    act(() => es.emit('progress', snapshot({ seq: 3, status: 'pausing' })))
    expect(qc.getQueryData<Job>(jobKeys.one('j1'))!.status).toBe('pausing')
  })

  it('AC7: connection state transitions', () => {
    const { hook, es } = setup()
    expect(hook.result.current.connection).toBe('reconnecting')
    act(() => es.open())
    expect(hook.result.current.connection).toBe('live')
    act(() => es.fail())
    expect(hook.result.current.connection).toBe('reconnecting')
    act(() => es.emit('progress', snapshot({ seq: 5 })))
    expect(hook.result.current.connection).toBe('live')
    act(() => es.fail(true))
    expect(hook.result.current.connection).toBe('offline')
  })

  it('AC7: completed closes and invalidates', () => {
    const onCompleted = vi.fn()
    const onErrorSample = vi.fn()
    const { qc, hook, es } = setup({ onCompleted, onErrorSample })
    const spy = vi.spyOn(qc, 'invalidateQueries')
    act(() => es.emit('error-sample', { code: 'PARSE_FAILED', message: 'bad' }))
    expect(onErrorSample).toHaveBeenCalledWith({ code: 'PARSE_FAILED', message: 'bad' })
    act(() => es.emit('completed', snapshot({ seq: 9, status: 'completed', percent: 100 })))
    expect(es.readyState).toBe(2)
    expect(hook.result.current.connection).toBe('offline')
    expect(qc.getQueryData<Job>(jobKeys.one('j1'))!.status).toBe('completed')
    expect(spy).toHaveBeenCalledWith({ queryKey: jobKeys.one('j1') })
    expect(onCompleted).toHaveBeenCalledTimes(1)
  })

  it('pauses after the tab is hidden for 5 minutes and resumes on return', () => {
    vi.useFakeTimers()
    try {
      setup()
      const first = FakeEventSource.latest()
      Object.defineProperty(document, 'hidden', { configurable: true, get: () => true })
      act(() => document.dispatchEvent(new Event('visibilitychange')))
      act(() => vi.advanceTimersByTime(5 * 60 * 1000))
      expect(first.readyState).toBe(2)
      Object.defineProperty(document, 'hidden', { configurable: true, get: () => false })
      act(() => document.dispatchEvent(new Event('visibilitychange')))
      expect(FakeEventSource.instances.length).toBe(2)
    } finally {
      vi.useRealTimers()
    }
  })
})
