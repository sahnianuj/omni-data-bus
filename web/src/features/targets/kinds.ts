import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, call } from '@/lib/api/client'
import type { components } from '@/lib/api/schema'

export type TargetKind = components['schemas']['TargetKind']
export type TargetKindCapabilities = components['schemas']['TargetKindCapabilities']
export type Location = components['schemas']['Location']
export type BundledTarget = components['schemas']['BundledTarget']
export type SharedFolder = components['schemas']['SharedFolder']

/** Couchbase capabilities, assumed while /target-kinds loads (1.0 behavior). */
export const couchbaseCapabilities: TargetKindCapabilities = {
  supportsDurability: true,
  supportsExpiry: true,
  supportsCreateLocation: true,
  requiresCredentials: true,
  maxDocBytes: 20 << 20,
  locationLevels: ['bucket', 'scope', 'collection'],
}

const targetKindsQuery = {
  queryKey: ['target-kinds'],
  staleTime: Infinity,
  queryFn: async () => call(api.GET('/api/v1/target-kinds')),
}

/** The target kinds this server supports, with what each can do (OC-036). */
export function useTargetKinds() {
  return useQuery({ ...targetKindsQuery, select: (d) => d.kinds })
}

/** The all-in-one image's bundled Couchbase node, offered as a target (OC-072); undefined elsewhere. */
export function useBundledTarget(): BundledTarget | undefined {
  return useQuery({ ...targetKindsQuery, select: (d) => d.bundled }).data
}

/** The folder of the user's computer shared into the all-in-one image (OC-080); undefined elsewhere. */
export function useSharedFolder(): SharedFolder | undefined {
  return useTargetKinds().data?.find((k) => k.type === 'directory')?.sharedFolder
}

/** Where a server path is on the user's computer ("" when it isn't under the shared folder). */
export function hostPathOf(path: string, shared: SharedFolder | undefined): string {
  if (!shared || !path) return ''
  const root = shared.path.replace(/\/+$/, '')
  if (path !== root && !path.startsWith(root + '/')) return ''
  const rest = path.slice(root.length)
  const win = shared.hostPath.includes('\\')
  return shared.hostPath.replace(/[\\/]+$/, '') + (win ? rest.replaceAll('/', '\\') : rest)
}

/** Capabilities of a target kind; Couchbase's until the kinds load (or for an unknown kind). */
export function useKindCapabilities(kind: string | undefined): TargetKindCapabilities {
  const kinds = useTargetKinds()
  return kinds.data?.find((k) => k.type === kind)?.kindCapabilities ?? couchbaseCapabilities
}

/** A location as users read it: "bucket.scope.collection" or "📁 orders/". */
export function formatLocation(loc: Location | undefined | null): string {
  if (!loc) return ''
  if (loc.kind === 'directory') return `📁 ${(loc.folder ?? '').replace(/^\/+|\/+$/g, '')}/`
  return [loc.bucket, loc.scope, loc.collection].join('.')
}

export type LocationNode = components['schemas']['LocationNode']

export const locationKeys = {
  list: (connId: string, parent: string) => ['locations', connId, parent] as const,
}

/** The children of a location on a stored target: sub-folders of a directory (OC-036/OC-038). */
export function useLocations(connId: string, parent: string) {
  return useQuery({
    queryKey: locationKeys.list(connId, parent),
    enabled: connId !== '',
    retry: false,
    queryFn: async () => (await call(api.GET('/api/v1/connections/{id}/locations', { params: { path: { id: connId }, query: { parent } } }))).nodes,
  })
}

/** Creates a location (a folder, or a Couchbase scope and collection). */
export function useCreateLocation(connId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (location: Location) => call(api.POST('/api/v1/connections/{id}/locations', { params: { path: { id: connId } }, body: location })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['locations', connId] }),
  })
}

export type FolderListing = components['schemas']['FolderListing']

/** The folder picker's view of the server (OC-038): the allowed areas (path ""), or a folder's sub-folders. */
export function useFolders(path: string, enabled = true) {
  return useQuery({
    queryKey: ['folders', path],
    enabled,
    retry: false,
    queryFn: () => call(api.GET('/api/v1/target-kinds/directory/folders', { params: { query: { path } } })),
  })
}

/** Creates a folder from the picker; resolves to its path. */
export function useMakeFolder() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { parent: string; name: string }) => call(api.POST('/api/v1/target-kinds/directory/folders', { body })),
    onSuccess: (_, v) => qc.invalidateQueries({ queryKey: ['folders', v.parent] }),
  })
}
