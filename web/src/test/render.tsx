import { render } from '@testing-library/react'
import type { ReactElement } from 'react'
import { createMemoryRouter, RouterProvider, type RouteObject } from 'react-router'
import { createQueryClient, Providers } from '@/app/providers'

/** Renders routes inside all app providers at the given URL. */
export function renderRoutes(routes: RouteObject[], url = '/') {
  const router = createMemoryRouter(routes, { initialEntries: [url] })
  const utils = render(
    <Providers queryClient={createQueryClient()}>
      <RouterProvider router={router} />
    </Providers>,
  )
  return { ...utils, router }
}

/** Renders a single element inside providers and a router. */
export function renderUI(ui: ReactElement, url = '/') {
  return renderRoutes([{ path: '*', element: ui }], url)
}
