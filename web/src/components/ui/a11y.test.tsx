import { render } from '@testing-library/react'
import axe from 'axe-core'
import type { ReactElement } from 'react'
import { describe, expect, it } from 'vitest'
import { AsyncBoundary } from '../AsyncBoundary'
import { ConfirmDialog } from '../ConfirmDialog'
import { EmptyState } from '../EmptyState'
import { ErrorState } from '../ErrorState'
import { PageHeader } from '../PageHeader'
import { Badge } from './badge'
import { Button } from './button'
import { Card, CardContent, CardHeader, CardTitle } from './card'
import { Input } from './input'
import { Label } from './label'

async function seriousViolations(ui: ReactElement) {
  const { container } = render(ui)
  // Color contrast is verified from the tokens (tokens.test.ts); jsdom can't compute it.
  const res = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false }, region: { enabled: false } } })
  void container
  return res.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical').map((v) => `${v.id}: ${v.help}`)
}

const cases: [string, ReactElement][] = [
  ['Button', <Button key="b">Save</Button>],
  ['Button (loading)', <Button key="bl" loading>Save</Button>],
  ['Input + Label', <div key="i"><Label htmlFor="h">Host</Label><Input id="h" /></div>],
  ['Badge', <Badge key="bd" tone="success">Connected</Badge>],
  ['Card', <Card key="c"><CardHeader><CardTitle>Title</CardTitle></CardHeader><CardContent>Body</CardContent></Card>],
  ['PageHeader', <PageHeader key="p" title="Projects" description="d" actions={<Button>New</Button>} />],
  ['EmptyState', <EmptyState key="e" title="Nothing" body="Add one" action={<Button>Add</Button>} />],
  ['ErrorState', <ErrorState key="er" error={new Error('boom')} onRetry={() => {}} />],
  ['AsyncBoundary (pending)', <AsyncBoundary key="a" query={{ status: 'pending', data: undefined, error: null, refetch: async () => ({}) as never }}>{() => null}</AsyncBoundary>],
  ['ConfirmDialog', <ConfirmDialog key="cd" open onOpenChange={() => {}} title="Delete?" confirmLabel="Delete" requireTypedName="x" onConfirm={() => {}} />],
]

describe('accessibility', () => {
  it.each(cases)('AC6: %s has no serious axe violations', async (_name, ui) => {
    expect(await seriousViolations(ui)).toEqual([])
  })
})
