import { test, expect } from '@playwright/test'

for (const width of [375, 1440]) test(`授权调整预览和结果 ${width}px`, async ({ page }, testInfo) => {
  await page.setViewportSize({ width, height: 1000 })
  await page.emulateMedia({ colorScheme: width === 375 ? 'dark' : 'light' })
  await page.goto('/users/usr-1')
  await page.getByRole('button', { name: '调整授权', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('checkbox', { name: '亚太优选 15 个节点', exact: true }).uncheck()
  await expect(dialog.getByRole('button', { name: '预览授权影响' })).toBeDisabled()
  await dialog.getByRole('checkbox', { name: '新加坡 6 个节点', exact: true }).check()
  await dialog.getByRole('button', { name: '预览授权影响' }).click()
  await expect(dialog.getByRole('heading', { name: '新增授权 0 个节点' })).toBeVisible()
  await expect(dialog.getByRole('heading', { name: '保留授权 6 个节点' })).toBeVisible()
  await expect(dialog.getByRole('heading', { name: '撤销授权 9 个节点' })).toBeVisible()
  await expect(dialog).toContainText('订阅链接保持不变')
  await expect(dialog).toContainText('失联节点恢复后继续撤销')
  await dialog.getByLabel('调整原因', { exact: true }).fill('只使用新加坡')
  await expect(dialog.getByRole('button', { name: '确认调整授权' })).toBeEnabled()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath(`grants-preview-${width}.png`), fullPage: true })
  await dialog.getByRole('button', { name: '返回选择' }).click()
  await expect(dialog.getByRole('checkbox', { name: '新加坡 6 个节点', exact: true })).toBeChecked()
  await dialog.getByRole('button', { name: '预览授权影响' }).click()
  await dialog.getByRole('button', { name: '确认调整授权' }).click()
  await expect(page.getByRole('heading', { name: '调整授权 · 阿川' })).toBeVisible()
  await expect(page.getByText('部分完成', { exact: true })).toBeVisible({ timeout: 10_000 })
  await expect(page.getByText('等待管理链路恢复')).toBeVisible()
  await page.getByRole('link', { name: '查看用户详情 →' }).click()
  await page.getByRole('button', { name: '调整授权', exact: true }).click()
  await expect(page.getByRole('checkbox', { name: '新加坡 6 个节点', exact: true })).toBeChecked()
})

test('授权预览冲突后可重新预览，不遗留待恢复请求', async ({ page }) => {
  let previewCount = 0, writes = 0
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    const reply = (body: unknown) => route.fulfill({ json: body })
    const { fixtures } = await import('../src/demo/fixtures')
    const data = fixtures(Date.now()), user = data.users[0]
    if (path === '/session') return reply({ admin_id: 'test', display_name: '测试管理员', csrf_token: 'test-csrf' })
    if (path === '/users/usr-1') return reply(user)
    if (path === '/groups') return reply(data.groups)
    if (path.endsWith('/subscription')) return reply({ state: 'published', published_nodes: 15, total_nodes: 15 })
    if (path.endsWith('/grants/preview')) {
      previewCount++
      return reply({ revision: 1, group_ids: ['sg'], added: [], retained: [{ id: 'sg-01', name: '新加坡 01' }], removed: [{ id: 'hk-01', name: '中国香港 01' }], changed: true, preview_hash: String(previewCount).repeat(64) })
    }
    if (path.endsWith('/grants')) {
      writes++
      return route.fulfill({ status: 409, json: { code: 'GRANT_PREVIEW_CHANGED', message: '节点组或授权状态已变化，请重新预览并确认' } })
    }
    return route.fulfill({ status: 404, json: { code: 'NOT_FOUND', message: '测试接口不存在' } })
  })
  await page.goto('http://127.0.0.1:14186/users/usr-1')
  await page.getByRole('button', { name: '调整授权', exact: true }).click()
  await page.getByRole('button', { name: '预览授权影响' }).click()
  await page.getByRole('button', { name: '确认调整授权' }).click()
  await expect(page.getByRole('alert')).toContainText('重新预览并确认')
  expect(await page.evaluate(() => sessionStorage.getItem('sbctl-pending-create'))).toBeNull()
  await page.getByRole('button', { name: '预览授权影响' }).click()
  await expect(page.getByRole('button', { name: '确认调整授权' })).toBeEnabled()
  expect(previewCount).toBe(2)
  expect(writes).toBe(1)
})
