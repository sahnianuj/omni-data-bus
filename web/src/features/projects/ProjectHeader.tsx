import { ArrowRight, Check, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router'
import { toast } from 'sonner'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { StatusDot } from '@/components/StatusDot'
import { Button } from '@/components/ui/button'
import { Can } from '@/features/auth/Can'
import { hasRole } from '@/features/auth/Can'
import { useSession } from '@/features/auth/api'
import { cn } from '@/lib/utils'
import { useDeleteProject, useUpdateProject, type Project, type RepoSummary } from './api'
import { ProjectStatusBadge } from './ProjectStatusBadge'
import { useAutosave } from './useAutosave'

function RepoChip({ role, repo }: { role: 'source' | 'target'; repo: RepoSummary | null }) {
  const { t } = useTranslation()
  const roleLabel = t(role === 'source' ? 'projects.roleSource' : 'projects.roleTarget')
  if (!repo) {
    return (
      <span className="flex h-[30px] items-center gap-2 rounded-full border border-dashed border-border-strong px-2.5 text-xs text-muted">
        <StatusDot status="untested" />
        {t('projects.notSaved', { role: roleLabel })}
      </span>
    )
  }
  const status = repo.lastTestStatus === 'ok' || repo.lastTestStatus === 'warn' ? 'ok' : repo.lastTestStatus === 'failed' ? 'failed' : 'untested'
  return (
    <span className="flex h-[30px] items-center gap-2 rounded-full border border-border bg-surface px-2.5 text-xs text-muted">
      <StatusDot status={status} />
      {t(`projects.types.${repo.type}`)} · <span className="font-mono">{repo.name}</span>
    </span>
  )
}

export function ProjectHeader({ project }: { project: Project }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const session = useSession()
  const update = useUpdateProject(project.id)
  const remove = useDeleteProject()
  const [name, setName] = useState(project.name)
  const [confirming, setConfirming] = useState(false)
  const canEdit = hasRole(session.data?.role, 'editor')
  const autosave = useAutosave<string>((value) => update.mutateAsync({ name: value, version: project.version }))

  return (
    <div className="flex h-[72px] shrink-0 items-center justify-between gap-4 border-b border-border bg-surface px-8">
      <div className="flex min-w-0 items-center gap-3">
        <input
          aria-label={t('projects.rename')}
          value={name}
          readOnly={!canEdit}
          onChange={(e) => {
            setName(e.target.value)
            if (e.target.value.trim()) autosave.schedule(e.target.value.trim())
          }}
          className="min-w-0 rounded-md border border-transparent bg-transparent px-1 text-xl font-semibold tracking-tight hover:border-border focus-visible:border-ring focus-visible:outline-none read-only:hover:border-transparent"
        />
        <ProjectStatusBadge status={project.status} />
        <span className={cn('flex items-center gap-1 text-xs text-subtle', autosave.state === 'error' && 'text-danger')} aria-live="polite">
          {autosave.state === 'saving' ? t('projects.saving') : null}
          {autosave.state === 'saved' ? (
            <>
              <Check className="size-3.5 text-success-dot" aria-hidden />
              {t('projects.saved')}
            </>
          ) : null}
          {autosave.state === 'error' ? (
            <button type="button" className="underline" onClick={autosave.retry}>
              {t('projects.notSavedRetry')}
            </button>
          ) : null}
        </span>
      </div>
      <div className="flex items-center gap-2">
        <RepoChip role="source" repo={project.source} />
        <ArrowRight className="size-4 text-subtle" aria-hidden />
        <RepoChip role="target" repo={project.target} />
        <Can role="editor">
          <Button variant="ghost" size="icon" aria-label={t('projects.delete')} onClick={() => setConfirming(true)}>
            <Trash2 aria-hidden />
          </Button>
        </Can>
      </div>
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t('projects.deleteTitle')}
        description={t('projects.deleteBody')}
        confirmLabel={t('projects.delete')}
        destructive
        requireTypedName={project.name}
        loading={remove.isPending}
        onConfirm={() =>
          remove.mutate(project.id, {
            onSuccess: () => {
              toast.success(t('projects.deleted'))
              navigate('/projects', { replace: true })
            },
          })
        }
      />
    </div>
  )
}
