import { expect, type Page } from '@playwright/test'
import { env, password, uid } from './env'

/** Page objects for the main screens (OC-034). Selectors prefer roles and labels. */

export class SignIn {
  constructor(private page: Page) {}
  async signUp(name = 'E2E User'): Promise<string> {
    const email = `e2e-${uid()}-${Date.now()}@example.com`
    await this.page.goto('/signup')
    await this.page.getByLabel('Your name').fill(name)
    await this.page.getByLabel('Email').fill(email)
    await this.page.getByLabel('Password', { exact: true }).fill(password)
    await this.page.getByRole('button', { name: 'Create account' }).click()
    await expect(this.page).not.toHaveURL(/signup/)
    return email
  }
  async signIn(email: string) {
    await this.page.goto('/signin')
    await this.page.getByLabel('Email').fill(email)
    await this.page.getByLabel('Password', { exact: true }).fill(password)
    await this.page.getByRole('button', { name: 'Sign in' }).click()
    await expect(this.page).not.toHaveURL(/signin/)
  }
}

export class Projects {
  constructor(private page: Page) {}
  async create(name: string) {
    await this.page.goto('/projects')
    await this.page.getByRole('button', { name: 'New project' }).first().click()
    await this.page.getByLabel('Name').fill(name)
    await this.page.getByRole('button', { name: 'Create project' }).click()
    await expect(this.page).toHaveURL(/\/projects\/[^/]+\/source$/)
    return this.page.url().split('/projects/')[1]!.split('/')[0]!
  }
}

export class ProjectWizard {
  constructor(private page: Page) {}

  async sourceStep() {
    const p = this.page
    const field = (name: string) => p.getByRole('textbox', { name, exact: true })
    await field('Host').fill(env.source.host)
    await field('Port').fill(String(env.source.port))
    await field('Database').fill(env.source.database)
    await field('Username').fill(env.source.username)
    await field('Password').fill(env.source.password)
    await p.getByRole('radio', { name: 'disable' }).check()
    await this.testAndSave()
  }

  async targetStep() {
    const p = this.page
    const field = (name: string) => p.getByRole('textbox', { name, exact: true })
    await field('Connection string').fill(env.target.connectionString)
    await field('Username').fill(env.target.username)
    await field('Password').fill(env.target.password)
    await this.testAndSave()
  }

  private async testAndSave() {
    const p = this.page
    await p.getByRole('button', { name: 'Test connection' }).click()
    await expect(p.getByText(/^Passed/).first()).toBeVisible({ timeout: 60_000 })
    await p.getByRole('button', { name: 'Save to project' }).click()
  }
}
