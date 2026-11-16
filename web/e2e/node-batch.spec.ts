import { test, expect, type Page } from '@playwright/test'
import { fixtures } from '../src/demo/fixtures'

type Reply = { value?: unknown; error?: { code: string; message: string; status: number } }
const pick = (page: Page, name: string, id: string) => page.getByRole('checkbox', { name: `选择 ${name} ${id}`, exact: true })
const bar = (page: Page) => page.getByRole('group', { name: '批量节点操作' })
const progress = (page: Page) => page.getByRole('region', { name: '批量任务进度' })
const line = (page: Page, name: string) => progress(page).getByRole('listitem').filter({ hasText: name })
// The demo backend keeps accepted node jobs and their request keys in session storage.
const saved = (page: Page) => page.evaluate(() => {
  const state = JSON.parse(sessionStorage.getItem('sbctl-demo-node-management-v1') ?? '{}') as { jobs?: [string, { kind: string; node_id: string }][]; requests?: [string, unknown][] }
  return { jobs: (state.jobs ?? []).map(([, job]) => `${job.kind}:${job.node_id}`).sort(), keys: (state.requests ?? []).map(([key]) => key) }
})
// Demo jobs always finish at once. To script queued, running and failed jobs, the client methods the batch uses are
// replaced inside the page (the dev server shares one module instance) and answered from the test process.
async function script(page: Page, backend: (name: string, ...args: unknown[]) => Reply) {
  await page.exposeFunction('nodeBatchBackend', backend)
  await page.evaluate(async () => {
    const backend = (window as unknown as { nodeBatchBackend: (name: string, ...args: unknown[]) => Promise<Reply> }).nodeBatchBackend
    const { api, ApiError } = await import(/* @vite-ignore */ new URL('/src/api/client.ts', location.origin).href)
    const call = async (name: string, ...args: unknown[]) => { const reply = await backend(name, ...args); if (reply.error) throw new ApiError(reply.error.code, reply.error.message, reply.error.status); return reply.value }
    const nodes = api.nodes
    api.nodes = async (...args: unknown[]) => (await call('nodes', ...args)) ?? nodes(...args)
    api.nodeConfig = (id: string) => call('config', id)
    api.submitNodeJob = (body: unknown, key: string) => call('submit', body, key)
    api.nodeJob = (id: string) => call('job', id)
  })
}

test('勾选两台节点批量检查，结果在离开页面后保留', async ({ page }) => {
  await page.goto('/nodes?region=sg')
  await expect(page.getByText('共 6 条记录')).toBeVisible()
  await expect(bar(page)).toHaveCount(0)
  await pick(page, '新加坡 01', 'sg-01').check()
  await pick(page, '新加坡 02', 'sg-02').check()
  await expect(bar(page).getByRole('status')).toHaveText('已选 2 台')
  await bar(page).getByRole('button', { name: '批量检查所选节点', exact: true }).click()
  await expect(progress(page).getByRole('heading', { name: '批量检查进度', exact: true })).toBeVisible()
  await expect(progress(page).getByRole('listitem')).toHaveCount(2)
  for (const name of ['新加坡 01', '新加坡 02']) { await expect(line(page, name)).toContainText('已完成'); await expect(line(page, name)).toContainText('1 项检查通过') }
  await expect(progress(page).getByRole('status')).toHaveText('已全部结束：2 台完成。')
  const state = await saved(page)
  expect(state.jobs).toEqual(['check:sg-01', 'check:sg-02'])
  expect(new Set(state.keys).size).toBe(2)
  for (const key of state.keys) expect(key).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)
  await expect(bar(page).getByRole('button', { name: '批量检查所选节点', exact: true })).toBeEnabled()
  await line(page, '新加坡 01').getByRole('link', { name: '新加坡 01', exact: true }).click()
  await expect(page.getByRole('heading', { name: '新加坡 01', exact: true })).toBeVisible()
  await expect(page.getByRole('region', { name: '节点管理任务' })).toContainText('检查节点')
  await page.goBack()
  await expect(line(page, '新加坡 02')).toContainText('已完成')
  await expect(bar(page)).toHaveCount(0)
  await progress(page).getByRole('button', { name: '清除批量任务结果', exact: true }).click()
  await expect(progress(page)).toHaveCount(0)
})

test('批量重启弹出确认框，取消时不提交任务', async ({ page }) => {
  await page.goto('/nodes?region=sg')
  await pick(page, '新加坡 01', 'sg-01').check()
  await pick(page, '新加坡 02', 'sg-02').check()
  await bar(page).getByRole('button', { name: '批量重启所选节点', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('heading', { name: '批量重启 2 台节点？', exact: true })).toBeVisible()
  for (const text of ['新加坡 01', 'sg-01', '新加坡 02', 'sg-02', '每台节点上的现有连接会短暂中断', '分批并行执行', '最多 3 台同时进行']) await expect(dialog).toContainText(text)
  await expect(dialog).not.toContainText('新加坡 03')
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
  await expect(dialog).toHaveCount(0)
  await expect(progress(page)).toHaveCount(0)
  await expect(bar(page).getByRole('status')).toHaveText('已选 2 台')
  // A restart sent by mistake would be saved before this later check completes.
  await bar(page).getByRole('button', { name: '批量检查所选节点', exact: true }).click()
  await expect(progress(page).getByRole('status')).toHaveText('已全部结束：2 台完成。')
  expect((await saved(page)).jobs).toEqual(['check:sg-01', 'check:sg-02'])
})

test('批量重启确认后显示进度并完成', async ({ page }) => {
  await page.goto('/nodes?region=sg')
  await pick(page, '新加坡 01', 'sg-01').check()
  await pick(page, '新加坡 02', 'sg-02').check()
  await bar(page).getByRole('button', { name: '批量重启所选节点', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: '确认重启', exact: true }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(progress(page).getByRole('heading', { name: '批量重启进度', exact: true })).toBeVisible()
  for (const name of ['新加坡 01', '新加坡 02']) await expect(line(page, name)).toContainText('已完成')
  await expect(progress(page).getByRole('status')).toHaveText('已全部结束：2 台完成。')
  const state = await saved(page)
  expect(state.jobs).toEqual(['restart:sg-01', 'restart:sg-02'])
  expect(new Set(state.keys).size).toBe(2)
})

test('手机视口勾选并批量检查 375px', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 375, height: 1000 })
  await page.goto('/nodes?region=sg')
  const card = (name: string) => page.locator('.mobile-record').filter({ hasText: name }).filter({ visible: true })
  await expect(page.locator('.mobile-record').filter({ visible: true })).toHaveCount(6)
  await card('新加坡 03').getByRole('checkbox', { name: '选择 新加坡 03 sg-03', exact: true }).check()
  await card('新加坡 05').getByRole('checkbox', { name: '选择 新加坡 05 sg-05', exact: true }).check()
  await expect(bar(page).getByRole('status')).toHaveText('已选 2 台')
  await bar(page).getByRole('button', { name: '批量重启所选节点', exact: true }).click()
  const bounds = await page.getByRole('dialog').boundingBox()
  expect(bounds!.x).toBeGreaterThanOrEqual(0)
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(375)
  await page.getByRole('dialog').getByRole('button', { name: '取消', exact: true }).click()
  await bar(page).getByRole('button', { name: '批量检查所选节点', exact: true }).click()
  for (const name of ['新加坡 03', '新加坡 05']) await expect(line(page, name)).toContainText('已完成')
  await expect(progress(page).getByRole('status')).toHaveText('已全部结束：2 台完成。')
  expect((await saved(page)).jobs).toEqual(['check:sg-03', 'check:sg-05'])
  for (const region of [bar(page), progress(page)]) { const box = await region.boundingBox(); expect(box!.x).toBeGreaterThanOrEqual(0); expect(box!.x + box!.width).toBeLessThanOrEqual(375) }
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.evaluate(() => window.scrollTo(0, 0))
  await page.screenshot({ path: testInfo.outputPath('node-batch-375.png'), fullPage: true })
})

test('全选本页，翻页或筛选变化后清空选择', async ({ page }) => {
  await page.goto('/nodes')
  await expect(page.getByText('共 20 条记录')).toBeVisible()
  const all = page.getByRole('checkbox', { name: '全选本页节点', exact: true }), chosen = page.getByRole('checkbox', { name: /^选择 /, checked: true })
  await all.check()
  await expect(bar(page).getByRole('status')).toHaveText('已选 8 台')
  await expect(chosen).toHaveCount(8)
  await page.getByRole('checkbox', { name: /^选择 / }).first().uncheck()
  await expect(bar(page).getByRole('status')).toHaveText('已选 7 台')
  await expect(all).not.toBeChecked()
  await page.getByText('全选本页', { exact: true }).click()
  await expect(bar(page).getByRole('status')).toHaveText('已选 8 台')
  await page.getByRole('button', { name: '下一页', exact: true }).click()
  await expect(page.getByText('2 / 3 页')).toBeVisible()
  await expect(bar(page)).toHaveCount(0)
  await expect(all).not.toBeChecked()
  await page.getByRole('button', { name: '上一页', exact: true }).click()
  await expect(page.getByText('1 / 3 页')).toBeVisible()
  await expect(page.getByRole('checkbox', { name: /^选择 / })).toHaveCount(8)
  await expect(chosen).toHaveCount(0)
  await all.check()
  await page.getByRole('combobox', { name: '筛选状态' }).click()
  await page.getByRole('option', { name: '在线', exact: true }).click()
  await expect(page.getByText('共 17 条记录')).toBeVisible()
  await expect(bar(page)).toHaveCount(0)
  await expect(chosen).toHaveCount(0)
  await all.check()
  await bar(page).getByRole('button', { name: '取消选择节点', exact: true }).click()
  await expect(bar(page)).toHaveCount(0)
  await expect(chosen).toHaveCount(0)
})

test('键盘勾选、确认框焦点与提交后的焦点', async ({ page }) => {
  await page.goto('/nodes?region=sg')
  const box = pick(page, '新加坡 01', 'sg-01')
  await box.focus(); await page.keyboard.press('Space')
  await expect(box).toBeChecked()
  await expect(box).toBeFocused()
  const restart = bar(page).getByRole('button', { name: '批量重启所选节点', exact: true }), check = bar(page).getByRole('button', { name: '批量检查所选节点', exact: true })
  await restart.focus(); await page.keyboard.press('Enter')
  await expect(page.getByRole('dialog')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(restart).toBeFocused()
  await expect(progress(page)).toHaveCount(0)
  await check.focus(); await page.keyboard.press('Enter')
  await expect(line(page, '新加坡 01')).toContainText('已完成')
  await expect(check).toBeFocused()
  await expect(check).toBeEnabled()
  expect((await saved(page)).jobs).toEqual(['check:sg-01'])
})

test('演示后端拒绝其中一台时，其余节点照常完成', async ({ page }) => {
  // The saved binding of sg-02 names a node the demo backend does not know, so its restart is refused on submit.
  await page.addInitScript(() => { if (!sessionStorage.getItem('sbctl-demo-node-management-v1')) sessionStorage.setItem('sbctl-demo-node-management-v1', JSON.stringify({ jobs: [], requests: [], configurations: [['sg-02', { draft: { id: 'sg-unknown', name: '新加坡 02' }, version: 4, binding: {}, checked_at: null, last_check: null, protocol_catalog: null }]] })) })
  await page.goto('/nodes?region=sg')
  await pick(page, '新加坡 01', 'sg-01').check()
  await pick(page, '新加坡 02', 'sg-02').check()
  await bar(page).getByRole('button', { name: '批量重启所选节点', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: '确认重启', exact: true }).click()
  await expect(line(page, '新加坡 01')).toContainText('已完成')
  await expect(line(page, '新加坡 02')).toContainText('失败')
  await expect(line(page, '新加坡 02')).toContainText('节点不存在')
  await expect(progress(page).getByRole('status')).toHaveText('已全部结束：1 台完成，1 台失败。')
  expect((await saved(page)).jobs).toEqual(['restart:sg-01'])
})

for (const width of [375, 1440]) test(`轮询到全部结束，单台失败不影响其它节点 ${width}px`, async ({ page }, testInfo) => {
  await page.setViewportSize({ width, height: 1000 })
  const stamp = new Date().toISOString(), refuse = (code: string, message: string, status = 409): Reply => ({ error: { code, message, status } })
  const jobs: Record<string, { id: string; kind: string; node_id: string; state: string; result: null; error_code: string | null; message: string | null; created_at: string; updated_at: string }> = {}
  const submits: { id: string; key: string; kind: string; version: number; inspection: string }[] = [], polls: Record<string, number[]> = {}
  let listed = 0
  await page.goto('/nodes?region=sg')
  await expect(page.getByText('共 6 条记录')).toBeVisible()
  await script(page, (name, ...args) => {
    if (name === 'nodes') { listed++; return {} }
    if (name === 'config') { const id = String(args[0]); return id === 'sg-03' ? refuse('API_UNAVAILABLE', '无法连接管理服务，请稍后重试。', 0) : { value: { draft: { id }, version: Number(id.slice(-1)) + 10 } } }
    if (name === 'submit') {
      const body = args[0] as { kind: string; draft: { id: string }; expected_version: number; inspection_id: string }, id = body.draft.id
      submits.push({ id, key: String(args[1]), kind: body.kind, version: body.expected_version, inspection: body.inspection_id })
      if (id === 'sg-02') return refuse('NODE_BUSY', '节点仍有配置或授权任务，请完成后操作')
      if (id === 'sg-05') return refuse('STALE_REVISION', '节点管理版本已变化，请重新读取')
      if (id === 'sg-06') return refuse('API_UNAVAILABLE', '无法连接管理服务，请稍后重试。', 0)
      jobs[`njob_${id}`] = { id: `njob_${id}`, kind: body.kind, node_id: id, state: 'queued', result: null, error_code: null, message: null, created_at: stamp, updated_at: stamp }
      return { value: { job_id: `njob_${id}` } }
    }
    const id = String(args[0]); (polls[id] ??= []).push(Date.now())
    return { value: jobs[id] }
  })
  await page.getByRole('checkbox', { name: '全选本页节点', exact: true }).check()
  const check = bar(page).getByRole('button', { name: '批量检查所选节点', exact: true }), restart = bar(page).getByRole('button', { name: '批量重启所选节点', exact: true }), clear = progress(page).getByRole('button', { name: '清除批量任务结果', exact: true })
  await restart.click()
  await page.getByRole('dialog').getByRole('button', { name: '确认重启', exact: true }).click()
  await expect(progress(page).getByRole('listitem')).toHaveCount(6)
  for (const [name, reason] of [['新加坡 02', '节点仍有配置或授权任务，请完成后操作'], ['新加坡 03', '无法连接管理服务，请稍后重试。'], ['新加坡 05', '节点管理版本已变化，请重新读取'], ['新加坡 06', '提交结果未确认，任务可能已经保存。']]) { await expect(line(page, name)).toContainText('失败'); await expect(line(page, name)).toContainText(reason) }
  for (const name of ['新加坡 01', '新加坡 04']) { await expect(line(page, name)).toContainText('排队'); await expect(line(page, name)).toContainText('任务已保存，等待执行') }
  await expect(progress(page).getByRole('status')).toHaveText('正在执行，4/6 台已结束。')
  for (const button of [check, restart, clear]) await expect(button).toBeDisabled()
  expect(submits.map(s => s.id).sort()).toEqual(['sg-01', 'sg-02', 'sg-04', 'sg-05', 'sg-06'])
  for (const s of submits) expect(s).toMatchObject({ kind: 'restart', version: Number(s.id.slice(-1)) + 10, inspection: '' })
  expect(new Set(submits.map(s => s.key)).size).toBe(5)
  jobs['njob_sg-01'].state = jobs['njob_sg-04'].state = 'running'
  for (const name of ['新加坡 01', '新加坡 04']) await expect(line(page, name)).toContainText('执行中')
  Object.assign(jobs['njob_sg-04'], { state: 'failed', error_code: 'NODE_UNREACHABLE', message: '节点连接失败，请恢复后重试' })
  await expect(line(page, '新加坡 04')).toContainText('失败')
  await expect(line(page, '新加坡 04')).toContainText('节点连接失败，请恢复后重试')
  await expect(progress(page).getByRole('status')).toHaveText('正在执行，5/6 台已结束。')
  await expect(line(page, '新加坡 01')).toContainText('执行中')
  for (const button of [check, restart, clear]) await expect(button).toBeDisabled()
  const before = listed
  Object.assign(jobs['njob_sg-01'], { state: 'completed', message: '操作已完成' })
  await expect(line(page, '新加坡 01')).toContainText('已完成')
  await expect(line(page, '新加坡 01')).toContainText('操作已完成')
  await expect(progress(page).getByRole('status')).toHaveText('已全部结束：1 台完成，5 台失败。')
  await expect.poll(() => listed).toBe(before + 1)
  for (const button of [check, restart, clear]) await expect(button).toBeEnabled()
  // Polling stops with the last job, and until then it runs about once a second for each job.
  const counted = JSON.stringify(polls)
  await page.waitForTimeout(2500)
  expect(JSON.stringify(polls)).toBe(counted)
  expect(listed).toBe(before + 1)
  expect(Object.keys(polls).sort()).toEqual(['njob_sg-01', 'njob_sg-04'])
  for (const times of Object.values(polls)) { expect(times.length).toBeGreaterThanOrEqual(3); for (let i = 1; i < times.length; i++) expect(times[i] - times[i - 1]).toBeGreaterThan(700) }
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.evaluate(() => window.scrollTo(0, 0))
  await page.screenshot({ path: testInfo.outputPath(`node-batch-failures-${width}.png`), fullPage: true })
  await clear.click()
  await expect(progress(page)).toHaveCount(0)
})

test('超过 20 台时提示并禁用批量操作', async ({ page }) => {
  const nodes = fixtures(Date.now()).nodes
  nodes.push({ ...nodes[0], id: 'sg-21', name: '新加坡 21' })
  await page.goto('/nodes')
  await expect(page.getByText('共 20 条记录')).toBeVisible()
  await script(page, name => name === 'nodes' ? { value: { items: nodes, total: nodes.length, page: 1, page_size: nodes.length } } : {})
  await page.getByRole('textbox', { name: '搜索节点名称或编号…' }).fill('全部')
  await expect(page.getByText('共 21 条记录')).toBeVisible()
  await page.getByRole('checkbox', { name: '全选本页节点', exact: true }).check()
  const check = bar(page).getByRole('button', { name: '批量检查所选节点', exact: true }), restart = bar(page).getByRole('button', { name: '批量重启所选节点', exact: true })
  await expect(bar(page).getByRole('status')).toHaveText('已选 21 台')
  await expect(page.getByRole('alert').filter({ hasText: '最多同时选择 20 台节点' })).toBeVisible()
  await expect(check).toBeDisabled()
  await expect(restart).toBeDisabled()
  await pick(page, '新加坡 21', 'sg-21').uncheck()
  await expect(bar(page).getByRole('status')).toHaveText('已选 20 台')
  await expect(page.getByRole('alert').filter({ hasText: '最多同时选择 20 台节点' })).toHaveCount(0)
  await expect(check).toBeEnabled()
  await expect(restart).toBeEnabled()
})
