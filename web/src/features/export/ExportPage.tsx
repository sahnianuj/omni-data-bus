import { Download, Upload } from 'lucide-react'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate, useOutletContext } from 'react-router'
import { toast } from 'sonner'
import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/button'
import { Can } from '@/features/auth/Can'
import type { Project } from '@/features/projects/api'
import { useImportReport } from './api'
import { ExportDialog } from './ExportDialog'
import { ExportHistory } from './ExportHistory'
import { HelpLink } from '@/components/HelpLink'

/** The project's exports (OC-055): export the migration to run elsewhere, see past exports, and
 * import the report.json of a run made with one. */
export function ExportPage() {
  const { t } = useTranslation()
  const project = useOutletContext<Project>()
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const importReport = useImportReport(project.id)
  const file = useRef<HTMLInputElement>(null)
  const onFile = async (f: File | undefined) => {
    if (!f) return
    try {
      const job = await importReport.mutateAsync(f)
      toast.success(t('export.imported'))
      void navigate(`/projects/${project.id}/jobs/${job.id}`)
    } finally {
      if (file.current) file.current.value = ''
    }
  }
  return (
    <div className="flex flex-col gap-5 px-8 py-7">
      <header className="flex flex-wrap items-start gap-3">
        <div className="flex flex-1 flex-col gap-1.5">
          <div className="flex items-center gap-1.5">
            <h2 className="text-lg font-semibold">{t('export.pageTitle')}</h2>
            <HelpLink topic="export" />
          </div>
          <p className="max-w-3xl text-[13px] text-muted">{t('export.pageIntro')}</p>
        </div>
        <Can role="editor">
          <Button variant="secondary" loading={importReport.isPending} onClick={() => file.current?.click()} title={t('export.importHint')}>
            <Upload aria-hidden />
            {t('export.import')}
          </Button>
        </Can>
        <Can role="editor">
          <Button onClick={() => setOpen(true)} disabled={(project.readyMappings ?? 0) === 0} title={(project.readyMappings ?? 0) === 0 ? t('export.noReady') : undefined}>
            <Download aria-hidden />
            {t('export.new')}
          </Button>
        </Can>
        <input ref={file} type="file" accept="application/json,.json" aria-label={t('export.import')} className="hidden" onChange={(e) => void onFile(e.target.files?.[0])} />
      </header>
      {importReport.error ? <ErrorState error={importReport.error} /> : null}
      <section aria-label={t('export.history.title')} className="flex flex-col gap-2">
        <h3 className="text-sm font-semibold">{t('export.history.title')}</h3>
        <ExportHistory projectId={project.id} />
      </section>
      <ExportDialog project={project} open={open} onOpenChange={setOpen} />
    </div>
  )
}
