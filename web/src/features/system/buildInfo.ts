import { useQuery } from '@tanstack/react-query'
import { useSession } from '@/features/auth/api'
import { api, call } from '@/lib/api/client'
import type { components } from '@/lib/api/schema'

export type VersionInfo = components['schemas']['Version']

/** Where release notes live; linked from the footer for release and rc builds. */
export const RELEASES_URL = 'https://github.com/sahnianuj/omni-data-bus/releases/tag/'

/** Drops the "v" of a tag ("v2.0.0" → "2.0.0"); empty becomes "dev" (mirrors Go buildinfo.Normalize). */
export function normalizeVersion(v: string | undefined): string {
  const s = (v ?? '').trim()
  if (!s) return 'dev'
  return /^v\d/.test(s) ? s.slice(1) : s
}

/** The release this SPA bundle was built for. */
export function spaVersion(): string {
  return normalizeVersion(import.meta.env.VITE_ODB_VERSION)
}

/**
 * The server's version when it differs from the SPA's: the binary serving this tab was upgraded
 * while it was open. A dev SPA (Vite) never prompts.
 */
export function staleVersion(server: string | undefined, spa = spaVersion()): string | null {
  if (!server || spa === 'dev') return null
  return normalizeVersion(server) === spa ? null : normalizeVersion(server)
}

/** The build info block for support tickets ("Copy build info"); `odb --version` prints the same lines. */
export function buildInfoText(v: VersionInfo, userAgent = navigator.userAgent): string {
  const name = `Omni-Data-Bus ${v.version ?? 'unknown'}${v.dirty ? ' (dirty)' : ''} (${v.channel ?? 'dev'})`
  const lines = [name]
  if (v.commit !== undefined) {
    lines.push(
      `Commit: ${shortCommit(v.commit)}  Built: ${v.buildTime || 'unknown'}  Go: ${v.goVersion ?? 'unknown'}  Platform: ${v.platform ?? 'unknown'}`,
    )
  }
  const nodes = nodeSummary(v)
  const parts = []
  if (v.metadataSchemaVersion !== undefined) parts.push(`Metadata schema: ${v.metadataSchemaVersion}`)
  if (nodes) parts.push(`Nodes: ${nodes}`)
  parts.push(`Browser: ${userAgent}`)
  lines.push(parts.join('   '))
  return lines.join('\n') + '\n'
}

/** "api 2.0.0, worker 1.0.1 / 2.0.0". */
export function nodeSummary(v: VersionInfo): string {
  if (!v.nodes) return ''
  return (['api', 'worker'] as const)
    .filter((r) => (v.nodes?.[r] ?? []).length > 0)
    .map((r) => `${r} ${(v.nodes?.[r] ?? []).join(' / ')}`)
    .join(', ')
}

export function shortCommit(c: string | undefined): string {
  return (c ?? '').slice(0, 7)
}

/**
 * GET /version. Signed-in and anonymous callers get different views, so the session is part of
 * the key; it refetches every 5 minutes and on focus so a tab notices an upgrade.
 */
export function useBuildInfo() {
  const session = useSession()
  const userId = session.data?.user.id ?? null
  return useQuery({
    queryKey: ['version', userId],
    enabled: !session.isPending,
    queryFn: () => call(api.GET('/api/v1/version')),
    staleTime: 60_000,
    refetchInterval: 5 * 60_000,
    refetchOnWindowFocus: true,
    retry: false,
  })
}
