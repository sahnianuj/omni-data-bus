import { ArrowRight, Box, Lock } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { toast } from 'sonner'
import { AsyncBoundary } from '@/components/AsyncBoundary'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { EmptyState } from '@/components/EmptyState'
import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/button'
import { hasRole } from '@/features/auth/Can'
import { useSession } from '@/features/auth/api'
import { useProjectConnections, type Project } from '@/features/projects/api'
import { ApiError } from '@/lib/api/client'
import { useBundledTarget } from '@/features/targets/kinds'
import { TargetKindPicker } from '@/features/targets/TargetKindPicker'
import { useSaveBundled, useSaveConnection, useTestStored, useTestUnsaved } from './api'
import { CapabilitiesCard, type Capabilities } from './CapabilitiesCard'
import { ConnectorPicker } from './ConnectorPicker'
import { MappingLocations } from './MappingLocations'
import { connectionUIs } from './registry'
import { RelocateDialog, type Relocate } from './RelocateDialog'
import { RepositorySummary } from './RepositorySummary'
import { TestChecklist, type TestResult } from './TestChecklist'
import type { Connection, ConnectionPayload } from './types'
import { HelpLink } from '@/components/HelpLink'

type Role = 'source' | 'target'

/**
 * A project's source or target repository (OC-009 / OC-012): pick a database type, fill the form,
 * Test connection, Save to project. Once saved it shows a summary that re-tests with the stored
 * credentials, so the password is never asked for again.
 */
export function RepositoryStep({ role, project }: { role: Role; project: Project }) {
  const connections = useProjectConnections(project.id)
  return <AsyncBoundary query={connections}>{(data) => <StepBody role={role} project={project} existing={data[role] ?? undefined} />}</AsyncBoundary>
}

function StepBody({ role, project, existing }: { role: Role; project: Project; existing?: Connection }) {
  const { t } = useTranslation()
  const session = useSession()
  const canEdit = hasRole(session.data?.role, 'editor')
  const [editing, setEditing] = useState(!existing)
  const [type, setType] = useState<string | null>(existing?.type ?? (role === 'source' ? 'postgres' : 'couchbase'))
  const [result, setResult] = useState<{ result: TestResult; saved: boolean; at: string } | null>(null)
  const [pending, setPending] = useState<ConnectionPayload | null>(null)
  // A kind change refused because mappings still point at the old kind (OC-068).
  const [moving, setMoving] = useState<{ type: string; payload: ConnectionPayload } | null>(null)
  const testUnsaved = useTestUnsaved(role)
  const save = useSaveConnection(project.id, role)
  const testStored = useTestStored(project.id)
  const bundled = useBundledTarget()
  const saveBundled = useSaveBundled(project.id)
  const ui = type ? connectionUIs[type] : undefined
  const error = testUnsaved.error ?? (moving ? null : save.error) ?? testStored.error ?? saveBundled.error
  const roleLabel = t(role === 'source' ? 'repo.sourceRole' : 'repo.targetRole')

  const runTest = (payload: ConnectionPayload) => {
    if (!type) return
    testUnsaved.mutate({ type, payload }, { onSuccess: (r) => setResult({ result: r, saved: false, at: new Date().toISOString() }) })
  }
  const doSave = (payload: ConnectionPayload, relocate?: Relocate) => {
    if (!type) return
    setPending(null)
    save.mutate(
      { type, payload, relocate },
      {
        onSuccess: ({ result: r }) => {
          setMoving(null)
          setResult({ result: r, saved: true, at: new Date().toISOString() })
          setEditing(false)
          toast.success(t('repo.saved', { role: roleLabel }))
        },
        onError: (e) => {
          if (role === 'target' && !relocate && e instanceof ApiError && e.code === 'TARGET_KIND_IN_USE') setMoving({ type, payload })
        },
      },
    )
  }
  const applyBundled = () =>
    saveBundled.mutate(undefined, {
      onSuccess: ({ result: r }) => {
        setType('couchbase')
        setResult({ result: r, saved: true, at: new Date().toISOString() })
        setEditing(false)
        toast.success(t('repo.saved', { role: roleLabel }))
      },
    })
  const requestSave = (payload: ConnectionPayload) => (result?.result.ok && !result.saved ? doSave(payload) : setPending(payload))

  if (!existing && !canEdit) {
    return (
      <div className="px-8 py-7">
        <EmptyState icon={<Lock />} title={t('repo.viewerTitle')} body={t('repo.viewerBody')} />
      </div>
    )
  }

  const next = role === 'source' ? { to: `/projects/${project.id}/target`, label: t('repo.nextTarget') } : { to: `/projects/${project.id}/tables`, label: t('repo.nextTables') }
  const ready = existing && (existing.lastTestStatus === 'ok' || existing.lastTestStatus === 'warn')

  return (
    <div className="flex min-h-full flex-col">
      <div className="flex flex-1 flex-col gap-5 px-8 py-7 xl:flex-row xl:gap-7">
        <div className="flex w-full max-w-[560px] flex-col gap-5">
          <div className="flex flex-col gap-1.5">
            <div className="flex items-center gap-1.5">
              <h2 className="text-lg font-semibold">{t(role === 'source' ? 'repo.sourceTitle' : 'repo.targetTitle')}</h2>
              {role === 'target' && type === 'directory' ? <HelpLink topic="directoryTarget" /> : null}
            </div>
            <p className="text-[13px] text-muted">
              {t('repo.intro')} {role === 'source' ? t('repo.sourceIntroExtra') : null}
            </p>
          </div>
          {editing || !existing ? (
            <>
              {role === 'target' ? (
                <>
                  <TargetKindPicker value={type} onChange={(v) => { setType(v); setResult(null) }} />
                  {bundled && type === 'couchbase' ? (
                    // The all-in-one image's own Couchbase: no address or password to type (OC-072).
                    <div className="flex items-center gap-2 text-[13px]">
                      <Button variant="secondary" size="sm" title={t('repo.bundledHint', { buckets: bundled.buckets.join(', ') })} loading={saveBundled.isPending} onClick={applyBundled}>
                        <Box aria-hidden />
                        {t('repo.useBundled')}
                      </Button>
                      <span className="text-muted">{t('repo.orEnter')}</span>
                    </div>
                  ) : null}
                </>
              ) : !existing ? (
                <ConnectorPicker role={role} value={type} onChange={(v) => { setType(v); setResult(null) }} />
              ) : null}
              {ui ? (
                <ui.Form
                  key={type}
                  existing={existing}
                  testing={testUnsaved.isPending}
                  saving={save.isPending}
                  onTest={runTest}
                  onSave={requestSave}
                  onCancel={existing ? () => setEditing(false) : undefined}
                />
              ) : null}
            </>
          ) : ui ? (
            <RepositorySummary
              conn={existing}
              ui={ui}
              testing={testStored.isPending}
              onEdit={() => {
                setResult(null)
                setEditing(true)
              }}
              onTest={() => testStored.mutate(existing.id, { onSuccess: (r) => setResult({ result: r, saved: true, at: new Date().toISOString() }) })}
              actions={
                ready ? (
                  <Button variant="link" size="sm" asChild>
                    <Link to={role === 'source' ? `/projects/${project.id}/source/schema` : `/projects/${project.id}/target/keyspaces`}>
                      {t(role === 'source' ? 'explorer.explore' : 'targets.browse')}
                      <ArrowRight aria-hidden />
                    </Link>
                  </Button>
                ) : undefined
              }
            />
          ) : null}
          {error ? <ErrorState error={error} /> : null}
          {role === 'target' && existing?.type === 'couchbase' && type === 'couchbase' ? (
            <MappingLocations projectId={project.id} connId={existing.id} canEdit={canEdit} />
          ) : null}
        </div>
        <div className="flex w-full max-w-[420px] flex-col gap-3">
          {role === 'target' && type === 'couchbase' && (result || existing) ? (
            <CapabilitiesCard caps={(result?.result.capabilities as Capabilities | undefined) ?? null} />
          ) : null}
          {result ? (
            <TestChecklist result={result.result} testedAt={result.at} saved={result.saved} />
          ) : editing ? (
            <div className="rounded-[var(--radius-card)] border border-dashed border-border-strong p-5 text-[13px] text-muted">{t('repo.notTested')}</div>
          ) : null}
          {editing ? (
            <div className="flex gap-2.5 rounded-lg bg-primary-soft px-3.5 py-3 text-xs leading-[18px] text-primary-soft-foreground">
              <Lock className="mt-0.5 size-4 shrink-0" aria-hidden />
              {t('repo.encryptedNote')}
            </div>
          ) : null}
        </div>
      </div>
      {ready && !editing ? (
        <footer className="sticky bottom-0 flex h-16 items-center justify-end border-t border-border bg-surface px-8">
          <Button asChild>
            <Link to={next.to}>
              {next.label}
              <ArrowRight aria-hidden />
            </Link>
          </Button>
        </footer>
      ) : null}
      {moving ? (
        <RelocateDialog
          projectId={project.id}
          to={moving.type}
          open
          saving={save.isPending}
          error={save.error instanceof ApiError && save.error.code === 'TARGET_KIND_IN_USE' ? null : save.error}
          onOpenChange={(o) => !o && setMoving(null)}
          onConfirm={(rel) => doSave(moving.payload, rel)}
        />
      ) : null}
      <ConfirmDialog
        open={pending !== null}
        onOpenChange={(o) => !o && setPending(null)}
        title={t('repo.confirmUntestedTitle')}
        description={t('repo.confirmUntestedBody')}
        confirmLabel={t('repo.saveAnyway')}
        onConfirm={() => pending && doSave(pending)}
      />
    </div>
  )
}
