import { readFileSync } from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import { installMatchMedia } from '@/test/matchMedia'

const html = readFileSync(path.resolve(__dirname, '../../index.html'), 'utf8')
// A file, not an inline script, so the CSP can be script-src 'self' (OC-033).
const script = readFileSync(path.resolve(__dirname, '../../public/theme-init.js'), 'utf8')

describe('index.html theme init script', () => {
  it('AC3: loads synchronously in <head>, before the app, and nothing is inline', () => {
    const head = html.slice(0, html.indexOf('</head>'))
    expect(head).toContain('<script src="/theme-init.js"></script>')
    expect(html).not.toMatch(/<script>/)
  })

  it('AC3: the script sets class from localStorage before React mounts', () => {
    window.localStorage.setItem('oc-theme', 'dark')
    new Function(script)()
    expect(document.documentElement).toHaveClass('dark')
  })

  it('AC3: system preference follows the OS', () => {
    installMatchMedia(true)
    new Function(script)()
    expect(document.documentElement).toHaveClass('dark')
  })

  it('AC3: light preference leaves the class off', () => {
    window.localStorage.setItem('oc-theme', 'light')
    installMatchMedia(true)
    new Function(script)()
    expect(document.documentElement).not.toHaveClass('dark')
  })
})
