import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { jobKeys, type Job } from '@/features/jobs/api'
import { api, ApiError, call, type Problem } from '@/lib/api/client'
import type { components } from '@/lib/api/schema'

export type ExportRequest = components['schemas']['ExportRequest']
export type ExportRecord = components['schemas']['ExportRecord']
export type ExportOptions = components['schemas']['ExportOptions']
export type ExternalRun = components['schemas']['ExternalRun']

export const exportKeys = {
  options: ['exports', 'options'] as const,
  history: (projectId: string) => ['exports', 'history', projectId] as const,
}

export function useExportOptions() {
  return useQuery({ queryKey: exportKeys.options, queryFn: () => call(api.GET('/api/v1/exports/options')), staleTime: 60_000 })
}

export function useExportHistory(projectId: string) {
  return useQuery({
    queryKey: exportKeys.history(projectId),
    queryFn: async () => (await call(api.GET('/api/v1/projects/{id}/exports', { params: { path: { id: projectId }, query: { limit: 50 } } }))).items,
  })
}

/** A finished export: the file to save and what the history records. */
/** A downloaded export; includedOdb is the platform of the odb in it ("" = none). */
export type ExportFile = { blob: Blob; fileName: string; bundleSha256: string; exportId: string; includedOdb: string }

function fileName(disposition: string | null, fallback: string): string {
  const m = disposition?.match(/filename="([^"]+)"/)
  return m?.[1] ?? fallback
}

/** POST /projects/{id}/exports: the download comes back as a file, never parsed as JSON. */
export function useCreateExport(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (body: ExportRequest): Promise<ExportFile> => {
      const { data, error, response } = await api.POST('/api/v1/projects/{id}/exports', {
        params: { path: { id: projectId } },
        body,
        parseAs: 'blob',
      })
      if (!response.ok) {
        let problem: Partial<Problem> = { title: response.statusText }
        if (error instanceof Blob) {
          try {
            problem = JSON.parse(await error.text()) as Problem
          } catch {
            // keep the status text
          }
        } else if (error && typeof error === 'object') {
          problem = error as Partial<Problem>
        }
        throw new ApiError(response.status, problem, response.headers.get('X-Request-ID') ?? undefined)
      }
      return {
        blob: data as Blob,
        fileName: fileName(response.headers.get('Content-Disposition'), 'export'),
        bundleSha256: response.headers.get('X-Odb-Bundle-Sha256') ?? '',
        exportId: response.headers.get('X-Odb-Export-Id') ?? '',
        includedOdb: response.headers.get('X-Odb-Included') ?? '',
      }
    },
    onSuccess: () => void qc.invalidateQueries({ queryKey: exportKeys.history(projectId) }),
  })
}

/** Saves a blob through the browser's download. */
export function saveFile(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.click()
  URL.revokeObjectURL(url)
}

/** POST /projects/{id}/runs/import with report.json as a multipart file (OC-055). */
export function useImportReport(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (file: File): Promise<Job> => {
      const form = new FormData()
      form.append('report', file)
      return call(
        api.POST('/api/v1/projects/{id}/runs/import', {
          params: { path: { id: projectId } },
          // multipart: the browser sets the boundary
          body: form as unknown as Record<string, never>,
          bodySerializer: (b: unknown) => b as FormData,
        }),
      ) as Promise<Job>
    },
    onSuccess: () => void qc.invalidateQueries({ queryKey: jobKeys.all }),
  })
}
