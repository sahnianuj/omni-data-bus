import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { CertDropzone, isPem } from './CertDropzone'

const pem = '-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n'

describe('CertDropzone', () => {
  it('AC5: rejects non-PEM file', async () => {
    const onChange = vi.fn()
    render(<CertDropzone label="CA certificate" onChange={onChange} />)
    // Content is checked, not just the name: a .pem file that isn't a certificate is rejected.
    fireEvent.drop(screen.getByTestId('cert-dropzone'), { dataTransfer: { files: [new File(['not a certificate'], 'ca.pem')] } })
    expect(await screen.findByRole('alert')).toHaveTextContent("isn't a PEM certificate")
    expect(onChange).toHaveBeenLastCalledWith(null)
  })

  it('AC5: accepts a PEM file via browse', async () => {
    const onChange = vi.fn()
    render(<CertDropzone label="CA certificate" onChange={onChange} />)
    await userEvent.upload(screen.getByLabelText('CA certificate'), new File([pem], 'ca.pem'))
    await waitFor(() => expect(onChange).toHaveBeenCalledWith(pem))
    expect(screen.getByText('ca.pem')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Remove' }))
    expect(onChange).toHaveBeenLastCalledWith(null)
  })

  it('AC5: accepts a dropped file', async () => {
    const onChange = vi.fn()
    render(<CertDropzone label="CA certificate" onChange={onChange} />)
    fireEvent.drop(screen.getByTestId('cert-dropzone'), { dataTransfer: { files: [new File([pem], 'dropped.crt')] } })
    await waitFor(() => expect(onChange).toHaveBeenCalledWith(pem))
  })

  it('AC5: shows the stored fingerprint, never the certificate', () => {
    render(<CertDropzone label="CA certificate" storedFingerprint="SHA256:ab:cd:ef:01:23:45:67:89:ab:cd:ef" onChange={() => {}} />)
    expect(screen.getByText(/SHA256:ab:cd/)).toBeInTheDocument()
  })

  it('isPem tolerates leading whitespace only', () => {
    expect(isPem('\n  ' + pem)).toBe(true)
    expect(isPem('x' + pem)).toBe(false)
  })
})
