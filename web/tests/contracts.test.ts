import { describe, expect, it } from 'vitest'
import { byteParts, totalBytes, usageChartScale } from '../src/lib/format'
import { createDemoService, paginate } from '../src/demo/transport'
const request = (path: string, method = 'GET', body?: unknown, key?: string) => new Request(`http://localhost/api/v1${path}`, { method, headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': 'synthetic-csrf', ...(key ? { 'Idempotency-Key': key } : {}) }, ...(body ? { body: JSON.stringify(body) } : {}) })
describe('整数累计与分页', () => {
  it('小于 1 MiB 的真实流量在图表中不能被截断成零', () => { const scale = usageChartScale([{ upload_bytes: '854', download_bytes: '3713' }]); expect(scale.unit).toBe('KiB'); expect(scale.value('854')).toBeCloseTo(0.833984375); expect(scale.value('3713')).toBeGreaterThan(3) })
  it('超过 JS 安全整数时仍精确保留字节', () => { expect(totalBytes({ upload_bytes: '9007199254740993', download_bytes: '7' })).toBe('9007199254741000'); expect(byteParts('1025')).toEqual({ value: '1.00', unit: 'KiB' }); expect(() => byteParts('-1')).toThrow() })
  it('按完整结果的 BigInt 排序后分页，不按字符串排序', () => { const rows = ['9', '100', '20', '9007199254740993'].map((upload_bytes, i) => ({ id: String(i), upload_bytes, download_bytes: '0' })); const page = paginate(rows, { sort: 'bytes', page: 2, page_size: 2 }); expect(page.items.map(r => r.upload_bytes)).toEqual(['20', '9']); expect(page.total).toBe(4) })
})
describe('F0 演示交互契约（不代表后端验收）', () => {
  it('授权调整先预览，保留历史和链接，重复提交找回原操作', async () => {
    let now = Date.now()
    const service = createDemoService(undefined, () => now)
    const old = await (await service(request('/users/usr-1'))).json()
    const link = await (await service(request('/users/usr-1/subscription-link', 'POST', undefined, 'before-link'))).json()
    const selection = { revision: old.revision, group_ids: ['sg'] }
    const preview = await (await service(request('/users/usr-1/grants/preview', 'POST', selection))).json()
    expect(preview.added).toHaveLength(0)
    expect(preview.retained).toHaveLength(6)
    expect(preview.removed).toHaveLength(9)
    expect(preview.preview_hash).toMatch(/^[a-f0-9]{64}$/)
    expect(await (await service(request('/users/usr-1'))).json()).toEqual(old)
    const body = { ...selection, preview_hash: preview.preview_hash, reason: '缩减授权' }
    expect((await service(request('/users/usr-1/grants', 'POST', { ...body, preview_hash: '0'.repeat(64) }, 'conflict-key'))).status).toBe(409)
    const accepted = await (await service(request('/users/usr-1/grants', 'POST', body, 'grants-key'))).json()
    expect(await (await service(request('/users/usr-1/grants', 'POST', body, 'grants-key'))).json()).toEqual(accepted)
    expect(await (await service(request('/requests/grants-key'))).json()).toEqual(accepted)
    now += 5000
    const user = await (await service(request('/users/usr-1'))).json()
    expect(user).toMatchObject({ id: old.id, group_ids: ['sg'], node_count: 6, created_at: old.created_at })
    expect(await (await service(request('/users/usr-1/subscription-link', 'POST', undefined, 'after-link'))).json()).toEqual(link)
    const operation = await (await service(request(`/operations/${accepted.operation_id}`))).json()
    expect(operation.kind).toBe('update_grants')
    expect(operation.state).toBe('partial')
    expect(operation.targets).toHaveLength(15)
  })
  it('重新启用沿用身份与历史，恢复请求并替代未完成的旧禁用', async () => {
    const map = new Map<string, string>()
    const storage = { getItem: (key: string) => map.get(key) ?? null, setItem: (key: string, value: string) => { map.set(key, value) } }
    let now = Date.now()
    let service = createDemoService(storage, () => now)
    const old = await (await service(request('/users/usr-3'))).json()
    const oldLink = await (await service(request('/users/usr-3/subscription-link', 'POST', undefined, 'old-link'))).json()
    const disabled = await (await service(request('/users/usr-3/disable', 'POST', { revision: old.revision, reason: '停用' }, 'disable-key'))).json()
    const body = { revision: old.revision + 1, reason: '恢复' }
    const enabled = await (await service(request('/users/usr-3/enable', 'POST', body, 'enable-key'))).json()
    expect(await (await service(request('/users/usr-3/enable', 'POST', body, 'enable-key'))).json()).toEqual(enabled)
    expect((await service(request('/users/usr-3/enable', 'POST', body, 'stale-key'))).status).toBe(409)
    const pending = await (await service(request('/users/usr-3'))).json()
    expect(pending.status).toBe('provisioning')
    expect(pending.subscription_state).toBe('pending')
    expect((await service(request('/users/usr-3/subscription-link', 'POST', undefined, 'early-link'))).status).toBe(409)
    now += 5000
    service = createDemoService(storage, () => now)
    expect(await (await service(request('/requests/enable-key'))).json()).toEqual(enabled)
    const user = await (await service(request('/users/usr-3'))).json()
    expect(user).toMatchObject({ id: old.id, status: 'active', created_at: old.created_at, group_ids: old.group_ids })
    expect((await (await service(request(`/operations/${disabled.operation_id}`))).json()).state).toBe('superseded')
    const newLink = await (await service(request('/users/usr-3/subscription-link', 'POST', undefined, 'new-link'))).json()
    expect(newLink.url).not.toBe(oldLink.url)
    expect((await (await service(request(`/operations/${enabled.operation_id}`))).json()).kind).toBe('enable_user')
  })
  it('新增返回 202，重复请求复用同一操作，改内容拒绝', async () => { const service = createDemoService(); const body = { display_name: '测试用户', note: '', group_ids: ['sg'] }; const first = await service(request('/users', 'POST', body, 'same-key')); expect(first.status).toBe(202); const result = await first.json(); expect(await (await service(request('/users', 'POST', body, 'same-key'))).json()).toEqual(result); expect((await service(request('/users', 'POST', { ...body, display_name: '不同用户' }, 'same-key'))).status).toBe(409); const op = await (await service(request(`/operations/${result.operation_id}`))).json(); expect(op.state).toBe('queued'); expect(op.targets.every((t: { state: string }) => t.state === 'queued')).toBe(true) })
  it('刷新后找回原任务，验证完成后状态前进', async () => { const map = new Map<string, string>(); const storage = { getItem: (key: string) => map.get(key) ?? null, setItem: (key: string, value: string) => { map.set(key, value) } }; let now = Date.now(); const first = createDemoService(storage, () => now); const body = { display_name: '恢复测试', note: '', group_ids: ['sg'] }; const accepted = await (await first(request('/users', 'POST', body, 'recover-key'))).json(); now += 5000; const reloaded = createDemoService(storage, () => now); expect(await (await reloaded(request('/requests/recover-key'))).json()).toEqual(accepted); const op = await (await reloaded(request(`/operations/${accepted.operation_id}`))).json(); expect(op.state).toBe('completed'); expect(op.targets).toHaveLength(6) })
  it('禁用保留身份，旧 revision 不能覆盖新状态，失联不能标全部完成', async () => { let now = Date.now(); const service = createDemoService(undefined, () => now); const old = await (await service(request('/users/usr-1'))).json(); const accepted = await (await service(request('/users/usr-1/disable', 'POST', { revision: old.revision, reason: '测试' }, 'disable-key'))).json(); const user = await (await service(request('/users/usr-1'))).json(); expect(user.status).toBe('disabled'); expect(user.id).toBe(old.id); expect((await service(request('/users/usr-1/disable', 'POST', { revision: old.revision, reason: '旧表单' }, 'stale-key'))).status).toBe(409); now += 5000; const op = await (await service(request(`/operations/${accepted.operation_id}`))).json(); expect(op.state).toBe('partial'); expect(op.targets.some((t: { state: string }) => t.state === 'unreachable')).toBe(true) })
})
