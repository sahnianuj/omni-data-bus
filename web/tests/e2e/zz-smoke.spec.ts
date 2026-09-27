import { expect, test } from '@playwright/test'
import { Api } from './support/api'

test('api helper smoke', async () => {
  const api = await Api.signUp()
  const p = await api.projectWithRepos()
  const m = await api.mappings(p.id, ['shop.customer'])
  expect(m.ids).toHaveLength(1)
  await api.dispose()
})
