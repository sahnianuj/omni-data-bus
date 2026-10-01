import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { mappingKeys } from '@/features/mapping/api'
import { projectKeys } from '@/features/projects/api'
import { api, call } from '@/lib/api/client'
import type { components } from '@/lib/api/schema'
import type { Relocate } from './RelocateDialog'
import type { ConnectionPayload } from './types'

export type ConnectorDescriptor = components['schemas']['ConnectorDescriptor']

export function useConnectors() {
  return useQuery({ queryKey: ['connectors'], staleTime: Infinity, queryFn: () => call(api.GET('/api/v1/connectors')) })
}


export function useTestUnsaved(role: 'source' | 'target') {
  return useMutation({
    mutationFn: ({ type, payload }: { type: string; payload: ConnectionPayload }) =>
      call(api.POST('/api/v1/connections/test', { body: { role, type, config: payload.config, secrets: payload.secrets } })),
  })
}

function useInvalidateProject(projectId: string) {
  const qc = useQueryClient()
  return () => {
    void qc.invalidateQueries({ queryKey: projectKeys.one(projectId) })
    void qc.invalidateQueries({ queryKey: projectKeys.connections(projectId) })
    void qc.invalidateQueries({ queryKey: projectKeys.list() })
    void qc.invalidateQueries({ queryKey: projectKeys.dashboard })
  }
}

/** Saves the repository, then re-tests it with the stored credentials so its status is recorded. */
export function useSaveConnection(projectId: string, role: 'source' | 'target') {
  const qc = useQueryClient()
  const invalidate = useInvalidateProject(projectId)
  return useMutation({
    mutationFn: async ({ type, payload, relocate }: { type: string; payload: ConnectionPayload; relocate?: Relocate }) => {
      const conn = await call(
        api.PUT('/api/v1/projects/{id}/connections/{role}', {
          params: { path: { id: projectId, role } },
          body: { type, config: payload.config, secrets: payload.secrets, relocate },
        }),
      )
      const result = await call(api.POST('/api/v1/connections/{id}/test', { params: { path: { id: conn.id } } }))
      return { conn, result }
    },
    onSettled: (_d, _e, vars) => {
      invalidate()
      // Moved mappings have new locations (OC-068).
      if (vars.relocate) void qc.invalidateQueries({ queryKey: mappingKeys.list(projectId) })
    },
  })
}

export function useTestStored(projectId: string) {
  const invalidate = useInvalidateProject(projectId)
  return useMutation({
    mutationFn: (connectionId: string) => call(api.POST('/api/v1/connections/{id}/test', { params: { path: { id: connectionId } } })),
    onSettled: invalidate,
  })
}
