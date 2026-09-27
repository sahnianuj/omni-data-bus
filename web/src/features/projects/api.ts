import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, call } from '@/lib/api/client'
import type { components } from '@/lib/api/schema'

export type Project = components['schemas']['Project']
export type RepoSummary = components['schemas']['RepoSummary']
export type ProjectConnections = components['schemas']['ProjectConnections']
export type Dashboard = components['schemas']['Dashboard']

export const projectKeys = {
  all: ['projects'] as const,
  list: () => ['projects', 'list'] as const,
  one: (id: string) => ['projects', id] as const,
  connections: (id: string) => ['projects', id, 'connections'] as const,
  dashboard: ['dashboard'] as const,
}

export function useProjects() {
  return useQuery({
    queryKey: projectKeys.list(),
    queryFn: async () => (await call(api.GET('/api/v1/projects', { params: { query: { limit: 100 } } }))).items,
  })
}

export function useProject(id: string) {
  return useQuery({ queryKey: projectKeys.one(id), queryFn: () => call(api.GET('/api/v1/projects/{id}', { params: { path: { id } } })) })
}

export function useProjectConnections(id: string) {
  return useQuery({
    queryKey: projectKeys.connections(id),
    queryFn: () => call(api.GET('/api/v1/projects/{id}/connections', { params: { path: { id } } })),
  })
}

export function useDashboard() {
  return useQuery({ queryKey: projectKeys.dashboard, queryFn: () => call(api.GET('/api/v1/dashboard')) })
}

export function useCreateProject() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: components['schemas']['CreateProjectRequest']) => call(api.POST('/api/v1/projects', { body })),
    onSuccess: (p) => {
      qc.setQueryData(projectKeys.one(p.id), p)
      void qc.invalidateQueries({ queryKey: projectKeys.list() })
      void qc.invalidateQueries({ queryKey: projectKeys.dashboard })
    },
  })
}

export function useUpdateProject(id: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: components['schemas']['UpdateProjectRequest']) =>
      call(api.PATCH('/api/v1/projects/{id}', { params: { path: { id } }, body })),
    onSuccess: (p) => {
      qc.setQueryData(projectKeys.one(id), p)
      void qc.invalidateQueries({ queryKey: projectKeys.list() })
    },
  })
}

export function useDeleteProject() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => call(api.DELETE('/api/v1/projects/{id}', { params: { path: { id } } })),
    onSuccess: (_, id) => {
      qc.removeQueries({ queryKey: projectKeys.one(id) })
      void qc.invalidateQueries({ queryKey: projectKeys.list() })
      void qc.invalidateQueries({ queryKey: projectKeys.dashboard })
    },
  })
}
