import type { RouteObject } from 'react-router'
import type { CrumbHandle } from '@/components/Breadcrumbs'
import { SignInPage } from '@/features/auth/SignInPage'
import { SignUpPage } from '@/features/auth/SignUpPage'
import { DashboardPage } from '@/features/dashboard/DashboardPage'
import { ExportPage } from '@/features/export/ExportPage'
import { SchemaExplorerPage } from '@/features/discovery/SchemaExplorerPage'
import { JobMonitorPage } from '@/features/jobs/JobMonitorPage'
import { JobsPage } from '@/features/jobs/JobsPage'
import { MigrateStep } from '@/features/jobs/MigrateStep'
import { ReviewStep } from '@/features/jobs/ReviewStep'
import { ProjectsPage } from '@/features/projects/ProjectsPage'
import { ProjectWizard } from '@/features/projects/ProjectWizard'
import { SourceStep } from '@/features/projects/steps/SourceStep'
import { TablesStep } from '@/features/projects/steps/TablesStep'
import { TargetStep } from '@/features/projects/steps/TargetStep'
import { AuditLogPage } from '@/features/settings/AuditLogPage'
import { SettingsPage } from '@/features/settings/SettingsPage'
import { KeyspacesPage } from '@/features/targets/KeyspacesPage'
import { MappingsStep } from '@/features/mapping/MappingsStep'
import { MappingWorkbenchPage } from '@/features/mapping/MappingWorkbench'
import { AuthedShell } from './AuthedShell'
import { NotFoundPage } from './NotFoundPage'
import { RouteErrorPage } from './RouteErrorPage'

const crumb = (key: string): CrumbHandle => ({ crumb: (t) => t(key) })

export const routes: RouteObject[] = [
  { path: '/signin', element: <SignInPage />, errorElement: <RouteErrorPage fullPage /> },
  { path: '/signup', element: <SignUpPage />, errorElement: <RouteErrorPage fullPage /> },
  {
    path: '/',
    element: <AuthedShell />,
    errorElement: <RouteErrorPage fullPage />,
    children: [
      {
        // Pathless boundary: a crash inside a page keeps the app shell (nav, breadcrumbs) usable.
        errorElement: <RouteErrorPage />,
        children: [
          { index: true, element: <DashboardPage />, handle: crumb('nav.dashboard') },
          {
            path: 'projects',
            handle: crumb('nav.projects'),
            children: [
              { index: true, element: <ProjectsPage /> },
              {
                path: ':projectId',
                element: <ProjectWizard />,
                children: [
                  { path: 'source', element: <SourceStep /> },
                  { path: 'source/schema', element: <SchemaExplorerPage /> },
                  { path: 'target', element: <TargetStep /> },
                  { path: 'target/keyspaces', element: <KeyspacesPage /> },
                  { path: 'tables', element: <TablesStep /> },
                  { path: 'mappings', element: <MappingsStep /> },
                  { path: 'mappings/:mid', element: <MappingWorkbenchPage /> },
                  { path: 'review', element: <ReviewStep /> },
                  { path: 'migrate', element: <MigrateStep /> },
                  { path: 'export', element: <ExportPage /> },
                  { path: 'jobs/:jobId', element: <JobMonitorPage /> },
                ],
              },
            ],
          },
          { path: 'jobs', element: <JobsPage />, handle: crumb('nav.jobs') },
          {
            path: 'settings',
            handle: crumb('nav.settings'),
            children: [
              { index: true, element: <SettingsPage /> },
              { path: 'audit', element: <AuditLogPage />, handle: crumb('audit.title') },
            ],
          },
          { path: '*', element: <NotFoundPage /> },
        ],
      },
    ],
  },
]
