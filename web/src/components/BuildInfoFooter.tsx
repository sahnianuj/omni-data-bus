import { Copy, ExternalLink, RefreshCw } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Badge } from '@/components/ui/badge'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import {
  buildInfoText,
  RELEASES_URL,
  shortCommit,
  staleVersion,
  useBuildInfo,
  type VersionInfo,
} from '@/features/system/buildInfo'

/** Localized build date ("2 Nov 2026"); the full UTC timestamp goes in the tooltip. */
function buildDate(iso: string | undefined, locale: string): string | null {
  if (!iso) return null
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return null
  return new Intl.DateTimeFormat(locale, {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
    timeZone: 'UTC',
  }).format(d)
}

function ChannelBadges({ v }: { v: VersionInfo }) {
  const { t } = useTranslation()
  return (
    <>
      {v.channel === 'dev' ? <Badge className="h-4 px-1.5 text-[10px]">{t('build.dev')}</Badge> : null}
      {v.channel === 'rc' ? (
        <Badge tone="primary" className="h-4 px-1.5 text-[10px]">
          {t('build.rc')}
        </Badge>
      ) : null}
      {v.dirty ? (
        <Badge tone="warning" className="h-4 px-1.5 text-[10px]">
          {t('build.dirty')}
        </Badge>
      ) : null}
      {v.consistent === false ? (
        <Badge tone="warning" className="h-4 px-1.5 text-[10px]">
          {t('build.mixed')}
        </Badge>
      ) : null}
    </>
  )
}

/**
 * The release and build of the running server, at the bottom of every page (OC-058). Signed in,
 * it opens a popover with every field, the running nodes and "Copy build info"; on the sign-in
 * page it shows the release only. It also notices when the server was upgraded under an open tab.
 */
export function BuildInfoFooter({ className }: { className?: string }) {
  const { t, i18n } = useTranslation()
  const info = useBuildInfo()
  const v = info.data
  if (!v || !v.version)
    return (
      <footer role="contentinfo" aria-label={t('build.label')} className={cn('h-6 shrink-0', className)} />
    )

  const full = v.commit !== undefined
  const date = buildDate(v.buildTime, i18n.language)
  const stale = staleVersion(v.version)
  const summary = [t('build.summary', { name: t('app.name'), version: v.version })]
  if (full && v.commit) summary.push(t('build.build', { commit: shortCommit(v.commit) }))
  if (date) summary.push(date)
  if (v.bundledStore) summary.push(t('build.bundled', { store: v.bundledStore }))

  return (
    <footer
      role="contentinfo"
      aria-label={t('build.label')}
      className={cn(
        'flex h-6 shrink-0 items-center gap-2 border-t border-border bg-surface px-4 text-[11px] text-muted',
        className,
      )}
    >
      {full ? (
        <Popover>
          <PopoverTrigger
            className="flex items-center gap-1.5 rounded px-1 hover:text-foreground focus-visible:outline-2 focus-visible:outline-primary"
            title={v.buildTime || undefined}
          >
            <span>{summary.join(' · ')}</span>
            <ChannelBadges v={v} />
          </PopoverTrigger>
          <PopoverContent side="top" align="start" aria-label={t('build.title')} className="w-[380px] text-[13px]">
            <BuildDetails v={v} />
          </PopoverContent>
        </Popover>
      ) : (
        <span className="flex items-center gap-1.5 px-1">
          <span>{summary.join(' · ')}</span>
          <ChannelBadges v={v} />
        </span>
      )}
      {stale ? (
        <span role="status" className="ml-auto flex items-center gap-2 text-foreground">
          {t('build.stale', { version: stale })}
          <button
            type="button"
            onClick={() => window.location.reload()}
            className="flex items-center gap-1 rounded font-medium text-primary hover:underline focus-visible:outline-2 focus-visible:outline-primary"
          >
            <RefreshCw className="size-3" aria-hidden />
            {t('build.reload')}
          </button>
        </span>
      ) : null}
    </footer>
  )
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted">{label}</dt>
      <dd className="min-w-0 break-words font-mono text-[12px]">{children}</dd>
    </>
  )
}

/** Every build field, the nodes, the release-notes link and "Copy build info" (popover and Settings → About). */
export function BuildDetails({ v }: { v: VersionInfo }) {
  const { t } = useTranslation()
  const copy = () => {
    void navigator.clipboard?.writeText(buildInfoText(v)).then(() => toast.success(t('build.copied')))
  }
  const nodes = (role: 'api' | 'worker') => (v.nodes?.[role] ?? []).join(', ') || '—'
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <span className="font-medium">{t('build.title')}</span>
        <ChannelBadges v={v} />
      </div>
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
        <Row label={t('build.fields.version')}>{v.version}</Row>
        <Row label={t('build.fields.channel')}>{v.channel}</Row>
        <Row label={t('build.fields.commit')}>{v.commit}</Row>
        <Row label={t('build.fields.built')}>{v.buildTime || t('build.unknown')}</Row>
        <Row label={t('build.fields.go')}>{v.goVersion}</Row>
        <Row label={t('build.fields.platform')}>{v.platform}</Row>
        <Row label={t('build.fields.schema')}>{v.metadataSchemaVersion ?? t('build.unknown')}</Row>
        {v.bundledStore ? <Row label={t('build.fields.bundled')}>{v.bundledStore}</Row> : null}
        <Row label={t('build.fields.api')}>{nodes('api')}</Row>
        <Row label={t('build.fields.worker')}>{nodes('worker')}</Row>
      </dl>
      {v.consistent === false ? (
        <p className="rounded-md bg-warning-soft px-2.5 py-2 text-[12px] text-warning">
          {t('build.mixedDetail')}
        </p>
      ) : null}
      <div className="flex items-center justify-between gap-2">
        {v.channel !== 'dev' && v.version ? (
          <a
            href={RELEASES_URL + 'v' + v.version}
            target="_blank"
            rel="noreferrer"
            className="flex items-center gap-1 text-primary hover:underline"
          >
            {t('build.releaseNotes')}
            <ExternalLink className="size-3" aria-hidden />
          </a>
        ) : (
          <span />
        )}
        <Button size="sm" variant="secondary" onClick={copy}>
          <Copy className="size-3.5" aria-hidden />
          {t('build.copy')}
        </Button>
      </div>
    </div>
  )
}
