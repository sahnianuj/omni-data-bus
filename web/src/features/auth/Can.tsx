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
export function Can({ role, children }: { role: Role; children: ReactElement<CanChildProps> }) {
  const { t } = useTranslation()
  const session = useSession()
  if (hasRole(session.data?.role, role)) return children
  if (!isValidElement(children)) return null
  return (
    <Tooltip content={t('auth.viewerReadOnly')}>
      <span tabIndex={0} className="inline-flex">
        {cloneElement(children, disabledProps(children.props))}
      </span>
    </Tooltip>
  )
}

type CanChildProps = { disabled?: boolean; asChild?: boolean; children?: React.ReactNode }

// A disabled link still navigates, so a link styled as a button (<Button asChild><Link/></Button>)
// becomes a real disabled button with the link's content. Other asChild wrappers (menu triggers)
// only get disabled.
function disabledProps(p: CanChildProps): CanChildProps {
  const inner = p.children
  if (p.asChild && isValidElement<LinkProps>(inner) && (inner.props.to !== undefined || inner.props.href !== undefined)) {
    return { disabled: true, asChild: false, children: inner.props.children }
  }
  return { disabled: true }
}

type LinkProps = { to?: unknown; href?: string; children?: React.ReactNode }
