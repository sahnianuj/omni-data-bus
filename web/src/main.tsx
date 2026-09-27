import '@fontsource/geist-sans/400.css'
import '@fontsource/geist-sans/500.css'
import '@fontsource/geist-sans/600.css'
import '@fontsource/geist-mono/400.css'
import './i18n'
import './styles/index.css'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { createBrowserRouter, RouterProvider } from 'react-router'
import { sessionKey } from './features/auth/api'
import { onUnauthorized } from './lib/api/client'
import { createQueryClient, Providers } from './app/providers'
import { routes } from './app/routes'

const router = createBrowserRouter(routes)
const queryClient = createQueryClient()

// Any 401 from the API (expired or revoked session) sends the user to sign-in and back afterwards.
onUnauthorized(() => {
  const { pathname, search } = window.location
  if (pathname === '/signin' || pathname === '/signup') return
  queryClient.clear()
  queryClient.setQueryData(sessionKey, null)
  void router.navigate(`/signin?next=${encodeURIComponent(pathname + search)}`, { replace: true })
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Providers queryClient={queryClient}>
      <RouterProvider router={router} />
    </Providers>
  </StrictMode>,
)
