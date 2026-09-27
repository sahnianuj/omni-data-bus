import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { useState } from 'react'
import { beforeEach, describe, expect, it } from 'vitest'
import { server, setupMswServer } from '@/test/msw'
import { renderUI } from '@/test/render'
import { ConnectorPicker } from './ConnectorPicker'

setupMswServer()
beforeEach(() =>
  server.use(
    http.get('*/api/v1/connectors', () =>
      HttpResponse.json({
        sources: [{ type: 'postgres', role: 'source', displayName: 'PostgreSQL', icon: 'postgres', capabilities: {} }],
        targets: [{ type: 'couchbase', role: 'target', displayName: 'Couchbase', icon: 'couchbase', capabilities: {} }],
      }),
    ),
  ),
)

function Harness({ role }: { role: 'source' | 'target' }) {
  const [v, setV] = useState<string | null>(null)
  return <ConnectorPicker role={role} value={v} onChange={setV} />
}

describe('ConnectorPicker', () => {
  it('AC2: renders available and coming-soon cards', async () => {
    renderUI(<Harness role="source" />)
    const pg = await screen.findByRole('radio', { name: /PostgreSQL/ })
    expect(pg).toBeEnabled()
    for (const name of ['MySQL', 'SQL Server', 'Oracle']) {
      expect(screen.getByRole('radio', { name: new RegExp(name) })).toBeDisabled()
    }
    expect(screen.getAllByText('Coming soon')).toHaveLength(3)
    await userEvent.click(pg)
    expect(pg).toHaveAttribute('aria-checked', 'true')
    expect(pg).toHaveTextContent('Selected')
  })

  it('AC2: target role lists target connectors', async () => {
    renderUI(<Harness role="target" />)
    expect(await screen.findByRole('radio', { name: /Couchbase/ })).toBeInTheDocument()
    expect(screen.queryByText('Coming soon')).not.toBeInTheDocument()
  })
})
