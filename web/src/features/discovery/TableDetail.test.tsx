import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { previewResult, shopSnapshot } from '@/test/discovery'
import { server, setupMswServer } from '@/test/msw'
import { renderUI } from '@/test/render'
import { TableDetail } from './TableDetail'

setupMswServer()

const table = (name: string) => shopSnapshot().schemas.flatMap((s) => s.tables).find((t) => t.name === name)!

describe('TableDetail', () => {
  it('shows an exact count without the estimate marker', () => {
    renderUI(<TableDetail connId="p1::source" schema="shop" table={{ ...table('customer'), rowEstimate: 20, rowCountExact: true }} onNavigate={() => {}} />)
    expect(screen.getByText('20 rows · 29.5 MB')).toBeInTheDocument()
  })

  it('AC4: columns with key glyphs', () => {
    renderUI(<TableDetail connId="p1::source" schema="shop" table={table('customer')} onNavigate={() => {}} />)
    expect(screen.getByRole('heading', { name: 'shop.customer' })).toBeInTheDocument()
    expect(screen.getByText('~120,345 rows · 29.5 MB')).toBeInTheDocument()
    const rows = screen.getAllByRole('row')
    const row = (name: string) => rows.find((r) => within(r).queryByText(name))!
    expect(within(row('customer_id')).getByLabelText('Primary key')).toBeInTheDocument()
    expect(within(row('email_address')).getByLabelText('Unique')).toBeInTheDocument()
    expect(within(row('address_id')).getByLabelText('Foreign key')).toBeInTheDocument()
    expect(within(row('search_vector')).getByLabelText('Unsupported type — values are sent as text')).toBeInTheDocument()
    expect(within(row('created_on')).getByText('timestamp with time zone')).toBeInTheDocument()
    expect(within(row('created_on')).getByText('datetime')).toBeInTheDocument()
  })

  it('AC4: relationships and indexes tabs', async () => {
    renderUI(<TableDetail connId="p1::source" schema="shop" table={table('customer')} onNavigate={() => {}} />)
    await userEvent.click(screen.getByRole('tab', { name: 'Relationships' }))
    expect(screen.getByText('customer_address_fk: shop.customer (address_id) → shop.address (address_id)')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('tab', { name: 'Indexes' }))
    expect(screen.getByText('customer_email_key')).toBeInTheDocument()
    expect(screen.getByText('Primary')).toBeInTheDocument()
  })

  it('AC4: clicking neighbor navigates', async () => {
    const onNavigate = vi.fn()
    renderUI(<TableDetail connId="p1::source" schema="shop" table={table('customer')} onNavigate={onNavigate} />)
    await userEvent.click(screen.getByRole('tab', { name: 'Relationships' }))
    await userEvent.click(screen.getByRole('button', { name: 'Open shop.orders' }))
    expect(onNavigate).toHaveBeenCalledWith('shop', 'orders')
    await userEvent.click(screen.getByRole('button', { name: 'Open shop.address' }))
    expect(onNavigate).toHaveBeenCalledWith('shop', 'address')
  })

  it('preview loads on demand with the selected row count', async () => {
    const limits: string[] = []
    server.use(
      http.get('*/api/v1/connections/:id/tables/:schema/:table/preview', ({ request, params }) => {
        limits.push(new URL(request.url).searchParams.get('limit')!)
        expect(params.table).toBe('customer')
        return HttpResponse.json(previewResult())
      }),
    )
    renderUI(<TableDetail connId="p1::source" schema="shop" table={table('customer')} onNavigate={() => {}} />)
    expect(limits).toEqual([])
    await userEvent.click(screen.getByRole('tab', { name: 'Preview' }))
    expect(await screen.findByRole('columnheader', { name: /preferences/ })).toBeInTheDocument()
    await userEvent.selectOptions(screen.getByLabelText('Rows'), '200')
    await vi.waitFor(() => expect(limits).toEqual(['50', '200']))
  })

  it('unreadable tables and stale estimates explain themselves', async () => {
    renderUI(<TableDetail connId="p1::source" schema="billing" table={table('invoice')} onNavigate={() => {}} />)
    expect(screen.getByText('No access')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('tab', { name: 'Preview' }))
    expect(screen.getByText(/can't SELECT from this table/)).toBeInTheDocument()

    renderUI(<TableDetail connId="p1::source" schema="shop" table={table('page_view')} onNavigate={() => {}} />)
    expect(screen.getAllByText('—').length).toBeGreaterThan(0)
  })
})
