import { useTranslation } from 'react-i18next'
import { Badge, type BadgeProps } from '@/components/ui/badge'
import type { Project } from './api'

const tone: Record<Project['status'], BadgeProps['tone']> = {
  draft: 'neutral', configured: 'primary', ready: 'primary', running: 'primary', completed: 'success', failed: 'danger',
}

export function ProjectStatusBadge({ status }: { status: Project['status'] }) {
  const { t } = useTranslation()
  return (
    <Badge tone={tone[status]}>
      <span aria-hidden className="size-1.5 rounded-full bg-current opacity-70" />
      {t(`projects.status.${status}`)}
    </Badge>
  )
}
