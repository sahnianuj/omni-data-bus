import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { projectKeys } from '@/features/projects/api'
import { api, call } from '@/lib/api/client'
import type { components } from '@/lib/api/schema'

export type Job = components['schemas']['Job']
export type JobStatus = components['schemas']['JobStatus']
export type JobOptions = components['schemas']['JobOptions']
export type ProgressSnapshot = components['schemas']['ProgressSnapshot']
export type MappingProgress = components['schemas']['MappingProgress']
export type PreflightResult = components['schemas']['PreflightResult']
export type PreflightCheck = components['schemas']['PreflightCheck']
export type JobEvent = components['schemas']['JobEvent']
export type JobError = components['schemas']['JobError']
export type JobErrorCount = components['schemas']['JobErrorCount']
export type ErrorSample = components['schemas']['ErrorSample']
export type RowErrorCode = components['schemas']['RowErrorCode']

export const jobKeys = {
  all: ['jobs'] as const,
  list: (projectId?: string) => ['jobs', 'list', projectId ?? 'all'] as const,
  one: (id: string) => ['job', id] as const,
  log: (id: string) => ['job', id, 'log'] as const,
  errors: (id: string) => ['job', id, 'errors'] as const,
}

export const terminalStatuses: JobStatus[] = ['cancelled', 'completed', 'completed_with_errors', 'failed']
export const isTerminal = (s: JobStatus) => terminalStatuses.includes(s)

/** Defaults shown in Advanced options; the server applies the same ones (jobs.DefaultOptions). */
export const defaultJobOptions: Required<Pick<JobOptions, 'writeMode' | 'batchSize' | 'readParallelism' | 'writeConcurrency' | 'tableConcurrency' | 'durability' | 'exactCount' | 'verifySampleSize'>> & {
  maxDocsPerSecond: number | null
  errorPolicy: { maxErrors: number; maxErrorRatio: number; minRowsForRatio: number }
} = {
  writeMode: 'upsert',
  batchSize: 1000,
  readParallelism: 'auto',
  writeConcurrency: 64,
  tableConcurrency: 3,
  maxDocsPerSecond: null,
  durability: 'none',
  errorPolicy: { maxErrors: 1000, maxErrorRatio: 0.01, minRowsForRatio: 10000 },
  exactCount: 'auto',
  verifySampleSize: 100,
}

export function useJobs(projectId?: string) {
  return useQuery({
    queryKey: jobKeys.list(projectId),
    queryFn: async () =>
      projectId
        ? (await call(api.GET('/api/v1/projects/{id}/jobs', { params: { path: { id: projectId }, query: { limit: 50 } } }))).items
        : (await call(api.GET('/api/v1/jobs', { params: { query: { limit: 50 } } }))).items,
    refetchInterval: 5000,
  })
}

export function useJob(id: string) {
  return useQuery({ queryKey: jobKeys.one(id), queryFn: () => call(api.GET('/api/v1/jobs/{jid}', { params: { path: { jid: id } } })) })
}

export function useJobLog(id: string, enabled = true) {
  return useQuery({
    queryKey: jobKeys.log(id),
    enabled,
    queryFn: async () => (await call(api.GET('/api/v1/jobs/{jid}/events/log', { params: { path: { jid: id } } }))).items,
  })
}

export function usePreflight(projectId: string) {
  return useMutation({
    mutationFn: (options: JobOptions) => call(api.POST('/api/v1/projects/{id}/jobs/preflight', { params: { path: { id: projectId } }, body: options })),
  })
}

function useInvalidateJobs(projectId?: string) {
  const qc = useQueryClient()
  return (job?: Job) => {
    void qc.invalidateQueries({ queryKey: jobKeys.all })
    if (job) qc.setQueryData(jobKeys.one(job.id), job)
    if (projectId) void qc.invalidateQueries({ queryKey: projectKeys.one(projectId) })
    void qc.invalidateQueries({ queryKey: projectKeys.list() })
  }
}

export function useCreateJob(projectId: string) {
  const invalidate = useInvalidateJobs(projectId)
  return useMutation({
    mutationFn: (options: JobOptions) => call(api.POST('/api/v1/projects/{id}/jobs', { params: { path: { id: projectId } }, body: options })),
    onSuccess: (res) => invalidate(res.job),
  })
}

type Control = 'pause' | 'resume' | 'cancel'

export function useJobControl(id: string, projectId?: string) {
  const invalidate = useInvalidateJobs(projectId)
  return useMutation({
    mutationFn: (action: Control) => {
      const params = { params: { path: { jid: id } } }
      if (action === 'pause') return call(api.POST('/api/v1/jobs/{jid}/pause', params))
      if (action === 'resume') return call(api.POST('/api/v1/jobs/{jid}/resume', params))
      return call(api.POST('/api/v1/jobs/{jid}/cancel', params))
    },
    onSuccess: (job) => invalidate(job),
  })
}

export type ResumeInfo = components['schemas']['ResumeInfo']
export type ResumeMode = 'original' | 'restart'

/** Resume a job (OC-029). "restart" cancels it and returns a new job with the current mappings. */
export function useResumeJob(id: string, projectId?: string) {
  const invalidate = useInvalidateJobs(projectId)
  return useMutation({
    mutationFn: (mode: ResumeMode) => call(api.POST('/api/v1/jobs/{jid}/resume', { params: { path: { jid: id } }, body: { mode } })),
    onSuccess: (job) => invalidate(job),
  })
}

/** Resuming needs a decision when a mapping changed, or a keyless table will be re-read. */
export const resumeNeedsDialog = (info?: ResumeInfo) => !!info && (info.changed.length > 0 || info.restart.length > 0)

export function useJobErrorSummary(id: string, enabled = true) {
  return useQuery({
    queryKey: [...jobKeys.errors(id), 'summary'],
    enabled,
    queryFn: () => call(api.GET('/api/v1/jobs/{jid}/errors/summary', { params: { path: { jid: id } } })),
  })
}

export function useJobErrors(id: string, code: string | null, enabled = true) {
  return useQuery({
    queryKey: [...jobKeys.errors(id), 'list', code ?? 'all'],
    enabled,
    queryFn: () => call(api.GET('/api/v1/jobs/{jid}/errors', { params: { path: { jid: id }, query: { limit: 100, ...(code ? { code } : {}) } } })),
  })
}

export function useRetryJob(id: string, projectId?: string) {
  const invalidate = useInvalidateJobs(projectId)
  return useMutation({
    mutationFn: (onlyCodes?: string[]) => call(api.POST('/api/v1/jobs/{jid}/retry', { params: { path: { jid: id } }, body: onlyCodes ? { onlyCodes } : {} })),
    onSuccess: (res) => invalidate(res.job),
  })
}

export type JobReport = components['schemas']['JobReport']
export type VerificationResult = components['schemas']['VerificationResult']
export type VerifyMismatch = components['schemas']['VerifyMismatch']

/** The job report with each table's verification (OC-030); fetched once the job has ended. */
export function useJobReport(id: string, enabled = true, verifies = true) {
  return useQuery({
    queryKey: [...jobKeys.one(id), 'report'],
    enabled,
    queryFn: () => call(api.GET('/api/v1/jobs/{jid}/report', { params: { path: { jid: id } } })),
    // Verification of the last table can finish just after the status turns final (bounded).
    refetchInterval: (q) => (verifies && q.state.dataUpdateCount < 20 && q.state.data?.mappings.some((m) => !m.verification) ? 3000 : false),
  })
}

export const reportUrl = (id: string, format: 'html' | 'json' | 'csv') =>
  `/api/v1/jobs/${encodeURIComponent(id)}/${format === 'json' ? 'report' : `report.${format}`}?download=1`

export const errorsExportUrl = (id: string, format: 'csv' | 'ndjson' = 'csv') => `/api/v1/jobs/${encodeURIComponent(id)}/errors/export?format=${format}`
