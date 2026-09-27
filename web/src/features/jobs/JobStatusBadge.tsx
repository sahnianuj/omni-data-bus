import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import type { JobStatus } from './api'

const tone: Record<JobStatus, 'neutral' | 'primary' | 'success' | 'warning' | 'danger'> = {
  queued: 'neutral', running: 'primary', pausing: 'warning', paused: 'warning', cancelling: 'neutral', cancelled: 'neutral',
  completed: 'success', completed_with_errors: 'warning', failed: 'danger',
}

export function JobStatusBadge({ status }: { status: JobStatus }) {
  const { t } = useTranslation()
  return <Badge tone={tone[status]}>{t(`jobStatus.${status}`)}</Badge>
}
