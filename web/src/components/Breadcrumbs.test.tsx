import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Outlet } from 'react-router'
import { renderRoutes } from '@/test/render'
import { Breadcrumbs, type CrumbHandle } from './Breadcrumbs'

const h = (s: string): CrumbHandle => ({ crumb: () => s })

describe('Breadcrumbs', () => {
  it('AC1: builds crumbs from route handles', () => {
    renderRoutes(
      [
        {
          path: '/',
          element: (
            <>
              <Breadcrumbs />
              <Outlet />
            </>
          ),
          children: [
            { path: 'projects', handle: h('Projects'), element: <Outlet />, children: [{ path: ':id', handle: h('Inventory sync'), element: null }] },
          ],
        },
      ],
      '/projects/123',
    )
    const nav = screen.getByRole('navigation', { name: 'Breadcrumb' })
    expect(nav).toHaveTextContent('Projects/Inventory sync')
    expect(screen.getByRole('link', { name: 'Projects' })).toHaveAttribute('href', '/projects')
    expect(screen.getByText('Inventory sync')).toHaveAttribute('aria-current', 'page')
  })
})
