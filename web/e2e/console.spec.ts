import { test, expect } from '@playwright/test'
import fs from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { fixtures } from '../src/demo/fixtures'
const qaDirectory = fileURLToPath(new URL('../../docs/qa/management-20261116/', import.meta.url))

test('全局筛选、分页、深链接和浏览器返回', async ({ page }) => {
  await page.goto('/nodes')
  await expect(page.getByRole('heading', { name: '节点与协议', exact: true })).toBeVisible()
  await expect(page.getByText('共 20 条记录')).toBeVisible()
  await page.getByRole('button', { name: '下一页', exact: true }).click()
  await expect(page).toHaveURL(/page=2/)
  await page.reload()
  await expect(page.getByText('2 / 3 页')).toBeVisible()
  await page.getByRole('combobox', { name: '筛选地区' }).click()
  await page.getByRole('option', { name: '新加坡', exact: true }).click()
  await expect(page.getByText('共 6 条记录')).toBeVisible()
  await page.getByRole('link', { name: '查看新加坡 01', exact: true }).click()
  await expect(page.getByRole('heading', { name: '新加坡 01', exact: true })).toBeVisible()
  await page.goBack()
  await expect(page.getByRole('combobox', { name: '筛选地区' })).toContainText('新加坡')
  await page.getByRole('textbox', { name: '搜索节点名称或编号…' }).fill('not-found')
  await expect(page.getByText('没有匹配的记录').first()).toBeVisible()
})
for (const width of [375, 1440]) test(`新增表单、持久操作恢复、订阅交付和禁用后重新启用 ${width}px`, async ({ page }, testInfo) => {
  await page.setViewportSize({ width, height: 1000 })
  await page.goto('/users')
  await page.getByRole('button', { name: '添加用户', exact: true }).click()
  await page.getByRole('button', { name: '提交配置', exact: true }).click()
  await expect(page.getByText('请输入显示名称')).toBeVisible()
  await page.getByLabel('显示名称', { exact: true }).fill('浏览器验收用户')
  await page.getByRole('checkbox', { name: '新加坡 6 个节点', exact: true }).check()
  await page.getByRole('button', { name: '提交配置', exact: true }).click()
  await expect(page).toHaveURL(/operations\/op-/)
  // On CI the task may already be running when navigation finishes; verify
  // its durable identity instead of requiring a transient queued badge.
  await expect(page.getByRole('heading', { name: '添加用户 · 浏览器验收用户' })).toBeVisible()
  await page.reload()
  await expect(page.getByText('已完成', { exact: true })).toBeVisible({ timeout: 10_000 })
  await page.getByRole('link', { name: '查看用户详情 →' }).click()
  await page.getByRole('button', { name: '获取订阅链接' }).click()
  await expect(page.getByText(/https:\/\/subscription.example.invalid.*surge.conf/)).toBeVisible()
  await expect(page.getByText(/https:\/\/subscription.example.invalid.*mihomo.yaml/)).toBeVisible()
  const oldLink = await page.getByText(/https:\/\/subscription.example.invalid.*surge.conf/).innerText()
  const userURL = page.url()
  expect(await page.evaluate(() => JSON.stringify(localStorage))).not.toContain('DEMO-NOT-A-SECRET')
  await page.getByRole('button', { name: '隐藏', exact: true }).first().click()
  await page.getByRole('button', { name: '禁用用户', exact: true }).click()
  await expect(page.getByRole('dialog')).toContainText('节点会重启')
  await page.getByRole('button', { name: '确认禁用', exact: true }).click()
  await expect(page.getByRole('heading', { name: '禁用用户 · 浏览器验收用户' })).toBeVisible()
  await expect(page.getByText('已完成', { exact: true })).toBeVisible({ timeout: 10_000 })
  await page.getByRole('link', { name: '查看用户详情 →' }).click()
  await expect(page).toHaveURL(userURL)
  await expect(page.getByRole('button', { name: '获取订阅链接' })).toBeDisabled()
  await page.getByRole('button', { name: '重新启用', exact: true }).click()
  await expect(page.getByRole('dialog')).toContainText('失联节点上的旧凭据仍需等待撤销')
  await expect(page.getByRole('dialog')).toContainText('共 6 个节点')
  await page.getByLabel('启用原因', { exact: true }).fill('恢复使用')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath('enable-dialog.png'), fullPage: true })
  await page.getByRole('button', { name: '确认重新启用', exact: true }).click()
  await expect(page.getByRole('heading', { name: '重新启用 · 浏览器验收用户' })).toBeVisible()
  await page.reload()
  await expect(page.getByText('已完成', { exact: true })).toBeVisible({ timeout: 10_000 })
  await page.getByRole('link', { name: '查看用户详情 →' }).click()
  await expect(page).toHaveURL(userURL)
  await expect(page.getByRole('button', { name: '禁用用户', exact: true })).toBeEnabled()
  await page.getByRole('button', { name: '获取订阅链接' }).click()
  await expect(page.getByText(/https:\/\/subscription.example.invalid.*surge.conf/)).toBeVisible()
  expect(await page.getByText(/https:\/\/subscription.example.invalid.*surge.conf/).innerText()).not.toBe(oldLink)
})
test('错误、空、过期状态和告警确认', async ({ page }) => {
  await page.goto('/components')
  await page.getByRole('combobox', { name: '数据反馈场景' }).click()
  await page.getByRole('option', { name: '读取失败', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('演示请求失败')
  await page.getByRole('button', { name: '重试', exact: true }).click()
  await expect(page.getByRole('combobox', { name: '数据反馈场景' })).toContainText('正常数据')
  await page.getByRole('combobox', { name: '数据反馈场景' }).click()
  await page.getByRole('option', { name: '过期且有历史缺口', exact: true }).click()
  await expect(page.getByText('采样已过期')).toBeVisible()
  await expect(page.getByText('历史有 2 处缺口')).toBeVisible()
  await page.goto('/alerts')
  await page.getByRole('button', { name: '确认已看到' }).first().click()
  await expect(page.locator('.alert-item').getByText('已确认', { exact: true }).first()).toBeVisible()
  await expect(page.getByText('远山仍有节点待撤销', { exact: true })).toBeVisible()
})
for (const width of [375, 1440]) test(`发现主机、只读检查、刷新恢复并确认接管 ${width}px`, async ({ page }) => {
  await page.setViewportSize({ width, height: 1000 })
  await page.goto('/nodes/new')
  await expect(page.getByRole('heading', { name: '添加节点', exact: true })).toBeVisible()
  await page.getByRole('button', { name: /香港新节点/ }).click()
  await expect(page.getByRole('textbox', { name: 'Tailscale 管理地址' })).toHaveValue('100.70.0.99')
  await page.getByRole('textbox', { name: '公网地址或域名（可选）', exact: true }).fill('hk.example.invalid')
  await page.getByRole('button', { name: '检查连接与容器', exact: true }).click()
  await expect(page.getByText('实际运行信息')).toBeVisible()
  await expect(page.getByText('sing-box', { exact: true }).first()).toBeVisible()
  await page.getByRole('textbox', { name: '节点 ID', exact: true }).fill('node_new_hk')
  await page.getByRole('textbox', { name: '节点名称', exact: true }).fill('香港新节点')
  await page.getByRole('textbox', { name: '地区代码', exact: true }).fill('hk')
  await page.getByRole('textbox', { name: '位置', exact: true }).fill('香港')
  await page.getByRole('textbox', { name: '节点组 ID', exact: true }).fill('grp_hk')
  await page.getByRole('textbox', { name: '节点组名称', exact: true }).fill('香港')
  await expect(page.getByRole('button', { name: '确认添加节点', exact: true })).toBeDisabled()
  await page.reload()
  await expect(page.getByText('实际运行信息')).toBeVisible()
  await expect(page.getByRole('textbox', { name: '节点 ID', exact: true })).toHaveValue('node_new_hk')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await fs.mkdir(qaDirectory, { recursive: true })
  await page.screenshot({ path: `${qaDirectory}setup-${width}.png`, fullPage: true })
  await page.getByRole('checkbox', { name: '我确认接管此节点，并接受必要时的短暂重连。' }).check()
  await page.getByRole('button', { name: '确认添加节点', exact: true }).click()
  await expect(page.getByText('节点已接入')).toBeVisible()
  await expect(page.getByText(/节点绑定已保存/)).toBeVisible()
  await page.getByRole('link', { name: '查看节点详情' }).click()
  await page.getByRole('button', { name: '编辑信息', exact: true }).click()
  await page.getByRole('dialog').getByRole('textbox', { name: '节点名称', exact: true }).fill('香港编辑测试')
  await page.getByRole('button', { name: '保存信息', exact: true }).click()
  await expect(page.locator('h1', { hasText: '香港编辑测试' })).toBeVisible()
  await page.getByRole('button', { name: '检查节点', exact: true }).click()
  await expect(page.getByText('通过 · 运行绑定')).toBeVisible()
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
test('侧栏折叠提示与日夜主题持久化、跟随系统', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button', { name: '收起侧栏', exact: true }).click()
  const nodes = page.getByRole('navigation', { name: '主导航' }).getByRole('link', { name: '节点与协议', exact: true })
  await nodes.hover()
  await expect(page.locator('[data-slot="tooltip-content"]')).toBeVisible()
  await expect(page.locator('[data-slot="tooltip-content"]')).toContainText('节点与协议')
  await nodes.click()
  await expect(page.getByRole('heading', { name: '节点与协议', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '切换深色主题', exact: true }).click()
  await expect(page.locator('html')).toHaveClass(/dark/)
  await page.reload()
  await expect(page.locator('html')).toHaveClass(/dark/)
  await page.getByRole('link', { name: '界面设置', exact: true }).click()
  await page.getByRole('button', { name: '跟随系统', exact: true }).click()
  await expect(page.locator('html')).not.toHaveClass(/dark/)
  await page.emulateMedia({ colorScheme: 'dark' })
  await expect(page.locator('html')).toHaveClass(/dark/)
  await expect(page.getByRole('button', { name: '切换浅色主题', exact: true })).toBeVisible()
})

test('生产构建未接入 API 时不使用演示数据', async ({ page }) => {
  await page.goto('http://127.0.0.1:14186')
  await expect(page.getByRole('alert')).toContainText('管理 API 尚未接入')
  await expect(page.getByText('演示模式')).toHaveCount(0)
  await expect(page.getByRole('heading', { name: '全局总览' })).toHaveCount(0)
})

test('生产登录按钮提交请求并显示来源错误', async ({ page }) => {
  let submissions = 0
  await page.route('**/api/v1/session', async route => {
    if (route.request().method() === 'POST') {
      submissions++
      expect(route.request().postDataJSON()).toEqual({ login_name: 'admin', password: 'test-only-password' })
      await route.fulfill({ status: 403, json: { code: 'ORIGIN_REJECTED', message: '请求来源不被允许' } })
    } else {
      await route.fulfill({ status: 401, json: { code: 'UNAUTHENTICATED', message: '请登录管理后台' } })
    }
  })
  await page.goto('http://127.0.0.1:14186')
  await page.getByLabel('管理员账号').fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-only-password')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('alert')).toHaveText('请求来源不被允许')
  expect(submissions).toBe(1)
})

for (const kind of ['create_user', 'disable_user', 'enable_user', 'update_grants'] as const) {
  test(`响应丢失后恢复原操作：${kind}`, async ({ page }) => {
    const data = fixtures(Date.now()), user = data.users[0]
    if (kind === 'enable_user') { user.status = 'disabled'; user.subscription_state = 'revoked' }
    let requestKey = '', writes = 0
    await page.route('**/api/v1/**', async route => {
      const request = route.request(), path = new URL(request.url()).pathname.replace('/api/v1', '')
      const reply = (body: unknown) => route.fulfill({ json: body })
      if (path === '/session') return reply({ admin_id: 'test-admin', display_name: '测试管理员', csrf_token: 'test-csrf' })
      if (path.endsWith('/grants/preview')) return reply({ revision: user.revision, group_ids: ['sg'], added: [], retained: [{ id: 'sg-01', name: '新加坡 01' }], removed: [{ id: 'hk-01', name: '中国香港 01' }], changed: true, preview_hash: 'a'.repeat(64) })
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
    } else if (kind === 'update_grants') {
      await page.getByRole('button', { name: '调整授权', exact: true }).click()
      await page.getByRole('button', { name: '预览授权影响', exact: true }).click()
      await page.getByRole('button', { name: '确认调整授权', exact: true }).click()
    } else if (kind === 'enable_user') {
      await page.getByRole('button', { name: '重新启用', exact: true }).click()
      await page.getByRole('button', { name: '确认重新启用', exact: true }).click()
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
    await expect(page.getByRole('region', { name: '板块入口' })).toBeVisible()
    await expect(page.getByLabel('流量筛选')).toHaveCount(0)
    const bodyFits = await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)
    expect(bodyFits).toBe(true)
    await fs.mkdir(qaDirectory, { recursive: true })
    await page.screenshot({ path: `${qaDirectory}overview-${width}-${colorScheme}.png`, fullPage: true })
    if (width === 375) {
      await page.getByRole('button', { name: '打开导航', exact: true }).click()
      await page.getByRole('navigation', { name: '移动导航' }).getByRole('link', { name: '用户与授权' }).click()
      await expect(page.getByRole('heading', { name: '用户与授权', exact: true })).toBeVisible()
      await expect(page.getByText('共 12 条记录')).toBeVisible()
      await expect(page.getByRole('dialog')).toHaveCount(0)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      await page.screenshot({ path: `${qaDirectory}users-${width}-${colorScheme}.png`, fullPage: true })
    }
    expect(errors).toEqual([])
  })
}

for (const width of [375, 1440]) test(`协议入口和节点本地用户用量分开显示 ${width}px`, async ({ page }, testInfo) => {
  await page.setViewportSize({width,height:1000})
  const f=fixtures(Date.now()), node=f.nodes[0]
  node.inbounds=[{tag:'vmess-local',protocol:'vmess',port:23323,upload_bytes:'1024',download_bytes:'4096',started_at:new Date().toISOString(),active:true}]
  node.total_inbounds=1;node.inbound_upload_bytes='1024';node.inbound_download_bytes='4096'
  node.local_users=[{name:'本地用户',upload_bytes:'512',download_bytes:'2048',started_at:new Date().toISOString(),active:true}]
  await page.route('**/api/v1/**', async route => {
    const path=new URL(route.request().url()).pathname
    if(path.endsWith('/session')) return route.fulfill({json:{admin_id:'test',display_name:'测试',csrf_token:'test'}})
    if(path.endsWith('/traffic')) return route.fulfill({json:{scope:'inbounds',trend:[],trend_from:'',trend_to:'',nodes:[node],periods:[{node_id:node.id,history_started_at:null,status:'cumulative'}],from:'',to:'',timezone:'Asia/Singapore',as_of:new Date().toISOString()}})
    return route.fulfill({status:404,json:{code:'NOT_FOUND',message:'test'}})
  })
  await page.goto('http://127.0.0.1:14186/usage')
  await expect(page.getByText('vmess-local', {exact:true}).filter({visible:true})).toBeVisible()
  await expect(page.getByText('5.00 KiB', {exact:true}).first()).toBeVisible()
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
  await page.screenshot({path:testInfo.outputPath(`inbounds-${width}.png`),fullPage:true})
  await page.getByRole('button',{name:'节点本地用户',exact:true}).click()
  await expect(page.getByText('本地用户',{exact:true}).filter({visible:true})).toBeVisible()
  await expect(page.getByText('2.00 KiB',{exact:true}).first()).toBeVisible()
  await expect(page.getByRole('group',{name:'统计范围'}).getByRole('button')).toHaveCount(3)
  await expect(page.getByRole('button',{name:'出站分流',exact:true})).toBeVisible()
  await expect(page.getByRole('button',{name:'sbctl 用户',exact:true})).toHaveCount(0)
})
