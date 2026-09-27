import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { cbCapabilities } from '@/test/connections'
import { CapabilitiesCard } from './CapabilitiesCard'

describe('CapabilitiesCard', () => {
  it('AC4: renders allowed/denied with reasons', () => {
    const { container } = render(<CapabilitiesCard caps={cbCapabilities} />)
    const allowed = [...container.querySelectorAll('[data-allowed="true"]')].map((e) => e.textContent)
    const denied = [...container.querySelectorAll('[data-allowed="false"]')].map((e) => e.textContent)
    expect(allowed).toEqual(expect.arrayContaining(['Write documents', 'Manage scopes and collections']))
    expect(denied[0]).toContain('Create buckets')
    expect(screen.getByText('Create buckets in the Capella UI, then pick them here.')).toBeInTheDocument()
  })

  it('AC4: unknown until tested', () => {
    render(<CapabilitiesCard caps={null} />)
    expect(screen.getByText('Test the connection to see what this credential can do.')).toBeInTheDocument()
  })
})
