import { AppShell } from '@/components/AppShell'
import { useSession } from '@/features/auth/api'
import { RequireAuth } from '@/features/auth/RequireAuth'
import { UserMenu } from '@/features/auth/UserMenu'

function Shell() {
  const session = useSession()
  return <AppShell userSlot={<UserMenu />} workspaceName={session.data?.workspace.name} />
}

/** The app shell for signed-in users; anonymous visitors are sent to sign-in. */
export function AuthedShell() {
  return (
    <RequireAuth>
      <Shell />
    </RequireAuth>
  )
}
