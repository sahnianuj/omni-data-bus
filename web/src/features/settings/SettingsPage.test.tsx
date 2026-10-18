import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { setupMswServer } from '@/test/msw'
import { renderUI } from '@/test/render'
import { mockSession } from '@/test/session'
import { SettingsPage } from './SettingsPage'

setupMswServer()

describe('SettingsPage', () => {
  it('OC-189 AC5: About shows the licence, copyright and author, and links the open-source licences', async () => {
    mockSession('viewer')
    renderUI(<SettingsPage />)
    const about = await screen.findByTestId('about-licence')
    expect(about).toHaveTextContent('Licensed under the Apache License 2.0 · Copyright 2026 Couchbase, Inc.')
    expect(about).toHaveTextContent('Author: Anuj Sahni, Principal Cloud & Solutions Architect')
    expect(screen.getByRole('link', { name: 'Open-source licences' })).toHaveAttribute('href', '/licenses.txt')
  })
})
