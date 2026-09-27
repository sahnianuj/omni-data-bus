import { cloneElement, isValidElement, type ReactElement } from 'react'
import { useTranslation } from 'react-i18next'
import { Tooltip } from '@/components/ui/tooltip'
import { useSession, type Role } from './api'

const rank: Record<Role, number> = { viewer: 1, editor: 2, owner: 3 }

/** hasRole reports whether role is at least min. */
export function hasRole(role: Role | undefined, min: Role) {
  return !!role && rank[role] >= rank[min]
}

/**
 * Wraps an action that needs at least `role`. For lower roles the child is disabled and
 * explains why in a tooltip (OC-005 AC6). The server enforces the same rule.
 */
export function Can({ role, children }: { role: Role; children: ReactElement<{ disabled?: boolean }> }) {
  const { t } = useTranslation()
  const session = useSession()
  if (hasRole(session.data?.role, role)) return children
  if (!isValidElement(children)) return null
  return (
    <Tooltip content={t('auth.viewerReadOnly')}>
      <span tabIndex={0} className="inline-flex">
        {cloneElement(children, { disabled: true })}
      </span>
    </Tooltip>
  )
}
