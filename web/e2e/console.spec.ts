import { test, expect } from '@playwright/test'
import fs from 'node:fs/promises'
import { fixtures } from '../src/demo/fixtures'

test('全局筛选、分页、深链接和浏览器返回', async ({ page }) => {
  await page.goto('/nodes')
  await expect(page.getByRole('heading', { name: '节点管理', exact: true })).toBeVisible()
  await expect(page.getByText('共 20 条记录')).toBeVisible()
  await page.getByRole('button', { name: '下一页', exact: true }).click()
  await expect(page).toHaveURL(/page=2/)
  await page.reload()
  await expect(page.getByText('2 / 3 页')).toBeVisible()
  await page.getByLabel('筛选地区').selectOption('sg')
  await expect(page.getByText('共 6 条记录')).toBeVisible()
  await page.getByRole('link', { name: '查看新加坡 01', exact: true }).click()
  await expect(page.getByRole('heading', { name: '新加坡 01', exact: true })).toBeVisible()
  await page.goBack()
  await expect(page.getByLabel('筛选地区')).toHaveValue('sg')
  await page.getByRole('textbox', { name: '搜索节点名称或编号…' }).fill('not-found')
  await expect(page.getByText('没有匹配的记录').first()).toBeVisible()
})
for (const width of [375, 1440]) test(`新增表单、持久操作恢复、订阅交付和禁用演示 ${width}px`, async ({ page }) => {
  await page.setViewportSize({ width, height: 1000 })
  await page.goto('/users')
  await page.getByRole('button', { name: '添加用户', exact: true }).click()
  await page.getByRole('button', { name: '提交配置', exact: true }).click()
  await expect(page.getByText('请输入显示名称')).toBeVisible()
  await page.getByLabel('显示名称', { exact: true }).fill('浏览器验收用户')
  await page.getByRole('checkbox', { name: '新加坡 6 个节点', exact: true }).check()
  await page.getByRole('button', { name: '提交配置', exact: true }).click()
  await expect(page).toHaveURL(/operations\/op-/)
  await expect(page.getByText('已接收', { exact: true }).first()).toBeVisible()
  await page.reload()
  await expect(page.getByText('已完成', { exact: true })).toBeVisible({ timeout: 10_000 })
  await page.getByRole('link', { name: '查看用户详情 →' }).click()
  await page.getByRole('button', { name: '获取订阅链接' }).click()
  await expect(page.getByText(/https:\/\/subscription.example.invalid/)).toBeVisible()
  expect(await page.evaluate(() => JSON.stringify(localStorage))).not.toContain('DEMO-NOT-A-SECRET')
  await page.getByRole('button', { name: '隐藏', exact: true }).click()
  await page.getByRole('button', { name: '禁用用户', exact: true }).click()
  await expect(page.getByRole('dialog')).toContainText('节点会重启')
  await page.getByRole('button', { name: '确认禁用', exact: true }).click()
  await expect(page.getByRole('heading', { name: '禁用用户 · 浏览器验收用户' })).toBeVisible()
  await expect(page.getByText('已完成', { exact: true })).toBeVisible({ timeout: 10_000 })
})
test('错误、空、过期状态和告警确认', async ({ page }) => {
  await page.goto('/components')
  await page.getByLabel('数据反馈场景').selectOption('error')
  await expect(page.getByRole('alert')).toContainText('演示请求失败')
  await page.getByRole('button', { name: '重试', exact: true }).click()
  await expect(page.getByLabel('数据反馈场景')).toHaveValue('ready')
  await page.getByLabel('数据反馈场景').selectOption('stale')
  await expect(page.getByText('采样已过期')).toBeVisible()
  await expect(page.getByText('历史有 2 处缺口')).toBeVisible()
  await page.goto('/alerts')
  await page.getByRole('button', { name: '确认已看到' }).first().click()
  await expect(page.locator('.alert-item').getByText('已确认', { exact: true }).first()).toBeVisible()
  await expect(page.getByText('远山仍有节点待撤销', { exact: true })).toBeVisible()
})
test('键盘弹窗焦点与注销', async ({ page }) => {
  await page.goto('/users')
  const add = page.getByRole('button', { name: '添加用户', exact: true })
  await add.focus(); await page.keyboard.press('Enter')
  await expect(page.getByRole('dialog')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(add).toBeFocused()
  await page.getByRole('button', { name: '退出登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '登录控制台' })).toBeVisible()
  await expect(page.getByText('浏览器验收用户')).toHaveCount(0)
})
test('生产构建未接入 API 时不使用演示数据', async ({ page }) => {
  await page.goto('http://127.0.0.1:14186')
  await expect(page.getByRole('alert')).toContainText('管理 API 尚未接入')
  await expect(page.getByText('演示模式')).toHaveCount(0)
  await expect(page.getByRole('heading', { name: '全局总览' })).toHaveCount(0)
})

for (const kind of ['create_user', 'disable_user'] as const) {
  test(`响应丢失后恢复原操作：${kind}`, async ({ page }) => {
    const data = fixtures(Date.now()), user = data.users[0]
    let requestKey = '', writes = 0
    await page.route('**/api/v1/**', async route => {
      const request = route.request(), path = new URL(request.url()).pathname.replace('/api/v1', '')
      const reply = (body: unknown) => route.fulfill({ json: body })
      if (path === '/session') return reply({ admin_id: 'test-admin', display_name: '测试管理员', csrf_token: 'test-csrf' })
      if (request.method() === 'POST') {
        writes++
        requestKey = request.headers()['idempotency-key']
        expect(requestKey).toBeTruthy()
        expect(request.headers()['x-csrf-token']).toBe('test-csrf')
        // The server accepted the write, but its response never reached the browser.
        return route.abort('connectionfailed')
      }
      if (path === '/groups') return reply(data.groups)
      if (path === '/users') return reply({ items: [user], total: 1, page: 1, page_size: 8 })
      if (path === `/users/${user.id}`) return reply(user)
      if (path.endsWith('/subscription')) return reply({ state: 'published', published_nodes: 6, total_nodes: 6 })
      if (path.startsWith('/requests/')) {
        expect(path).toBe(`/requests/${requestKey}`)
        return reply({ operation_id: 'recovered-operation' })
      }
      if (path === '/operations/recovered-operation') return reply({ ...data.operations[0], id: 'recovered-operation', kind, user_id: user.id, subject: '响应恢复测试', state: 'completed' })
      return route.fulfill({ status: 404, json: { code: 'NOT_FOUND', message: '测试接口不存在' } })
    })
    await page.goto(`http://127.0.0.1:14186${kind === 'create_user' ? '/users' : `/users/${user.id}`}`)
    if (kind === 'create_user') {
      await page.getByRole('button', { name: '添加用户', exact: true }).click()
      await page.getByLabel('显示名称', { exact: true }).fill('响应恢复测试')
      await page.getByRole('checkbox', { name: '新加坡 6 个节点', exact: true }).check()
      await page.getByRole('button', { name: '提交配置', exact: true }).click()
    } else {
      await page.getByRole('button', { name: '禁用用户', exact: true }).click()
      await page.getByRole('button', { name: '确认禁用', exact: true }).click()
    }
    await expect(page.getByRole('dialog').getByRole('alert')).toContainText('无法连接管理服务')
    const pending = await page.evaluate(() => JSON.parse(sessionStorage.getItem('sbctl-pending-create')!))
    expect(pending.key).toBe(requestKey)
    expect(pending.kind).toBe(kind)
    expect(Object.keys(pending).sort()).toEqual(kind === 'create_user' ? ['key', 'kind'] : ['key', 'kind', 'target_id'])
    if (kind === 'create_user') await expect(page.getByRole('button', { name: '提交配置', exact: true })).toBeDisabled()
    await page.reload()
    await expect(page.getByText('上次提交的结果尚未确认', { exact: true })).toBeVisible()
    await page.getByRole('button', { name: '找回操作', exact: true }).click()
    await expect(page).toHaveURL(/operations\/recovered-operation/)
    await expect(page.getByRole('heading', { name: /响应恢复测试/ })).toBeVisible()
    expect(writes).toBe(1)
    expect(await page.evaluate(() => sessionStorage.getItem('sbctl-pending-create'))).toBeNull()
  })
}

test('手机横屏与 200% 浏览器缩放等效视口下表单可操作', async ({ page }) => {
  // Desktop page zoom reduces the CSS viewport; CSS zoom does not emulate that behavior.
  for (const viewport of [{ width: 812, height: 375 }, { width: 720, height: 500 }]) {
    await page.setViewportSize(viewport)
    await page.goto('/users')
    await page.getByRole('button', { name: '添加用户', exact: true }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()
    await page.getByLabel('显示名称', { exact: true }).fill('小屏表单测试')
    await page.getByRole('checkbox', { name: '新加坡 6 个节点', exact: true }).check()
    await page.getByRole('button', { name: '取消', exact: true }).scrollIntoViewIfNeeded()
    const bounds = await dialog.boundingBox()
    expect(bounds!.y).toBeGreaterThanOrEqual(0)
    expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(viewport.height + 1)
    await page.getByRole('button', { name: '取消', exact: true }).click()
    await expect(dialog).toHaveCount(0)
  }
})
for (const width of [375, 768, 1440]) for (const colorScheme of ['light', 'dark'] as const) {
  test(`视觉 ${width}px ${colorScheme}`, async ({ page }) => {
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    await page.setViewportSize({ width, height: width < 800 ? 1000 : 1100 })
    await page.emulateMedia({ colorScheme, reducedMotion: 'reduce' })
    await page.goto('/')
    await expect(page.getByRole('heading', { name: '全局总览', exact: true })).toBeVisible()
    await expect(page.getByRole('img', { name: /用户已记录累计/ })).toBeVisible()
    const bodyFits = await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)
    expect(bodyFits).toBe(true)
    await fs.mkdir('../docs/qa/f0', { recursive: true })
    await page.screenshot({ path: `../docs/qa/f0/overview-${width}-${colorScheme}.png`, fullPage: true })
    if (width === 375) {
      await page.getByRole('button', { name: '打开导航', exact: true }).click()
      await page.getByRole('navigation', { name: '移动导航' }).getByRole('link', { name: '用户与授权' }).click()
      await expect(page.getByRole('heading', { name: '用户与授权', exact: true })).toBeVisible()
      await expect(page.getByText('共 12 条记录')).toBeVisible()
      await expect(page.getByRole('dialog')).toHaveCount(0)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      await page.screenshot({ path: `../docs/qa/f0/users-${width}-${colorScheme}.png`, fullPage: true })
    }
    expect(errors).toEqual([])
  })
}
