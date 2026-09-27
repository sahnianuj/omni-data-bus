import { readFileSync } from 'node:fs'
import path from 'node:path'
import { parse } from 'yaml'
import { describe, expect, it } from 'vitest'

type Service = {
  image?: string
  restart?: string
  healthcheck?: { test?: string[] }
  environment?: Record<string, string>
  secrets?: string[]
  read_only?: boolean
  cap_drop?: string[]
  stop_grace_period?: string
  profiles?: string[]
}
const file = path.resolve(__dirname, '../../deploy/compose/prod.yml')
const raw = readFileSync(file, 'utf8')
const compose = parse(raw, { merge: true }) as { services: Record<string, Service>; secrets: Record<string, { file: string }> }
const app = compose.services['omni-data-bus']!

describe('production compose', () => {
  it('AC3: prod services have healthchecks, restart policy, no default secrets', () => {
    for (const [name, svc] of Object.entries(compose.services)) {
      expect(svc.healthcheck?.test, `${name} needs a healthcheck`).toBeDefined()
      expect(svc.restart, `${name} restarts`).toBe('unless-stopped')
    }
    // The app's secrets come from files (Docker secrets), never inline values.
    expect(app.environment?.OC_MASTER_KEY_FILE).toBe('/run/secrets/master_key')
    expect(app.environment?.OC_META_PASSWORD_FILE).toBe('/run/secrets/meta_password')
    expect(app.environment).not.toHaveProperty('OC_MASTER_KEY')
    expect(app.environment).not.toHaveProperty('OC_META_PASSWORD')
    expect(app.environment).not.toHaveProperty('OC_DEV')
    expect(app.secrets).toEqual(['master_key', 'meta_password'])
    expect(raw).not.toMatch(/password\s*[:=]\s*\S*password/i)
    expect(raw).not.toMatch(/OC_MASTER_KEY\s*[:=]\s*[A-Za-z0-9+/]{20,}/)
  })

  it('AC3: the app runs hardened, probes itself, and has time to drain', () => {
    expect(app.healthcheck?.test).toEqual(['CMD', '/usr/local/bin/odb', 'healthcheck'])
    expect(app.read_only).toBe(true)
    expect(app.cap_drop).toEqual(['ALL'])
    expect(app.stop_grace_period).toBe('45s')
  })

  it('AC3: no database container; Caddy is optional', () => {
    const images = Object.values(compose.services).map((s) => s.image ?? '')
    expect(images.some((i) => /couchbase|postgres/.test(i))).toBe(false)
    expect(compose.services.caddy?.profiles).toEqual(['tls'])
  })
})
