import { ArrowLeft, Boxes } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useOutletContext } from 'react-router'
import { EmptyState } from '@/components/EmptyState'
import { Button } from '@/components/ui/button'
import type { Project } from '@/features/projects/api'
import { emptyKeyspace, KeyspacePicker, type KeyspaceValue } from './KeyspacePicker'

/** /projects/:projectId/target/keyspaces — browse and create keyspaces on the project's target (OC-013, OC-014). */
export function KeyspacesPage() {
  const { t } = useTranslation()
  const project = useOutletContext<Project>()
  const [value, setValue] = useState<KeyspaceValue>(emptyKeyspace)
  const back = (
    <Button variant="ghost" size="sm" asChild>
      <Link to={`/projects/${project.id}/target`}>
        <ArrowLeft aria-hidden />
        {t('targets.back')}
      </Link>
    </Button>
  )
  if (!project.target) {
    return (
      <div className="px-8 py-7">
        <EmptyState icon={<Boxes />} title={t('targets.noTargetTitle')} body={t('targets.noTargetBody')} action={back} />
      </div>
    )
  }
  return (
    <div className="flex flex-col gap-5 px-8 py-7">
      <div>{back}</div>
      <div className="flex flex-col gap-1.5">
        <h2 className="text-lg font-semibold">{t('targets.title')}</h2>
        <p className="max-w-2xl text-[13px] text-muted">{t('targets.intro')}</p>
      </div>
      <div className="max-w-4xl rounded-[var(--radius-card)] border border-border bg-surface p-5">
        <KeyspacePicker connId={project.target.id} value={value} onChange={setValue} />
      </div>
    </div>
  )
}
