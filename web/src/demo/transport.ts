import type { Schema, Filters } from '../api/types'
import { fixtures } from './fixtures'

type State = ReturnType<typeof fixtures> & { requests: Record<string, { signature: string; operation_id: string }>; signedIn: boolean }
const storageKey = 'sbctl-f0-synthetic-state-v1'
const totalBytes = (r: { upload_bytes: string; download_bytes: string }) => BigInt(r.upload_bytes) + BigInt(r.download_bytes)
export function paginate<T extends { id?: string; user_id?: string | null; title?: string; name?: string; display_name?: string; subject?: string; status?: string; state?: string; region?: string; upload_bytes?: string; download_bytes?: string; created_at?: string }>(items: T[], f: Filters) {
  const filtered = items.filter(row => (!f.q || `${row.name ?? row.display_name ?? row.subject ?? row.title ?? ''} ${row.id ?? ''}`.toLowerCase().includes(f.q.toLowerCase())) && (!f.status || (row.status ?? row.state) === f.status) && (!f.region || row.region === f.region))
  filtered.sort((a, b) => {
    if (f.sort === 'bytes') {
      const x = BigInt(a.upload_bytes ?? 0) + BigInt(a.download_bytes ?? 0), y = BigInt(b.upload_bytes ?? 0) + BigInt(b.download_bytes ?? 0)
      if (x !== y) return x > y ? -1 : 1
    }
    if (f.sort === 'created' && a.created_at !== b.created_at) return (b.created_at ?? '').localeCompare(a.created_at ?? '')
    return (a.name ?? a.display_name ?? a.id ?? '').localeCompare(b.name ?? b.display_name ?? b.id ?? '', 'zh-CN') || (a.id ?? a.user_id ?? '').localeCompare(b.id ?? b.user_id ?? '')
  })
  const pageSize = Math.min(50, Math.max(1, f.page_size ?? 8)), page = Math.max(1, f.page ?? 1)
  return { items: filtered.slice((page - 1) * pageSize, page * pageSize), total: filtered.length, page, page_size: pageSize }
}
export function createDemoService(storage?: Pick<Storage, 'getItem' | 'setItem'>, now = () => Date.now()) {
  let state: State = { ...fixtures(now()), requests: {}, signedIn: true }
  try { const saved = storage?.getItem(storageKey); if (saved) { const parsed = JSON.parse(saved) as State; if (parsed.marker === state.marker) state = parsed } } catch { /* Synthetic demo can reset if browser storage is unavailable. */ }
  const save = () => { try { storage?.setItem(storageKey, JSON.stringify(state)) } catch { /* In-memory demo still works. */ } }
  const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' } })
  const fail = (code: string, message: string, status = 400) => json({ code, message }, status)
  function advance() {
    for (const op of state.operations.filter(op => op.state === 'queued' || op.state === 'running')) {
      const age = now() - Date.parse(op.created_at)
      if (age < 1000) continue
      op.state = 'running'
      op.targets.forEach(t => { const online = state.nodes.find(n => n.id === t.node_id)?.status !== 'offline'; t.state = !online ? 'unreachable' : age > 4000 ? 'verified' : 'applied'; t.message = !online ? '等待管理链路恢复' : age > 4000 ? '演示验证完成' : '演示配置已应用，等待验证' })
      if (age > 4000) {
        op.state = op.targets.some(t => t.state === 'unreachable') ? 'partial' : 'completed'
        op.subscription_state = op.kind === 'disable_user' ? 'revoked' : 'published'
        const user = state.users.find(u => u.id === op.user_id)
        if (user) { user.subscription_state = op.subscription_state; if (op.kind === 'create_user') user.status = 'active' }
      }
    }
    save()
  }
  return async (req: Request): Promise<Response> => {
    advance()
    const url = new URL(req.url), p = decodeURIComponent(url.pathname.replace('/api/v1', '')), method = req.method
    const body = req.body && req.headers.get('content-type')?.includes('json') ? await req.json() : undefined
    if (p === '/session') {
      if (method === 'DELETE') { state.signedIn = false; save(); return new Response(null, { status: 204 }) }
      if (method === 'POST') state.signedIn = true
      if (!state.signedIn) return fail('UNAUTHENTICATED', '请登录管理后台。', 401)
      save(); return json({ admin_id: 'demo-admin', display_name: '管理员', csrf_token: 'synthetic-csrf' } satisfies Schema['Session'])
    }
    if (!state.signedIn) return fail('UNAUTHENTICATED', '请重新登录。', 401)
    if (method !== 'GET' && req.headers.get('X-CSRF-Token') !== 'synthetic-csrf') return fail('CSRF_FAILED', '请求校验失败。', 403)
    const key = req.headers.get('Idempotency-Key')
    if (method === 'POST' && !key) return fail('KEY_REQUIRED', '缺少请求标识。')
    const signature = JSON.stringify({ method, path: p, body })
    if (key && state.requests[key]) return state.requests[key].signature === signature ? json({ operation_id: state.requests[key].operation_id }, 202) : fail('HASH_CONFLICT', '同一请求标识不能用于不同操作。', 409)
    const f: Filters = { q: url.searchParams.get('q') ?? '', status: url.searchParams.get('status') ?? '', region: url.searchParams.get('region') ?? '', page: Number(url.searchParams.get('page') ?? 1), page_size: Number(url.searchParams.get('page_size') ?? 8), sort: (url.searchParams.get('sort') ?? 'name') as Filters['sort'] }
    const usage = (): Schema['Usage'][] => state.users.map(u => ({ user_id: u.id, display_name: u.display_name, node_count: u.node_count, upload_bytes: u.upload_bytes, download_bytes: u.download_bytes, quality: u.quality }))
    if (p === '/overview') return json({ node_count: state.nodes.length, online_count: state.nodes.filter(n => n.status === 'online').length, degraded_count: state.nodes.filter(n => n.status === 'degraded').length, offline_count: state.nodes.filter(n => n.status === 'offline').length, user_count: state.users.filter(u => u.status !== 'disabled').length, pending_count: state.operations.filter(op => op.state !== 'completed').length, upload_bytes: state.users.reduce((sum, u) => sum + BigInt(u.upload_bytes), 0n).toString(), download_bytes: state.users.reduce((sum, u) => sum + BigInt(u.download_bytes), 0n).toString(), regions: [...new Set(state.nodes.map(n => n.region))].map(id => ({ id, name: state.nodes.find(n => n.region === id)!.location, node_count: state.nodes.filter(n => n.region === id).length, online_count: state.nodes.filter(n => n.region === id && n.status === 'online').length })), operations: state.operations.slice(0, 3), alerts: state.alerts.filter(a => a.state === 'open'), top_usage: usage().sort((a, b) => totalBytes(a) > totalBytes(b) ? -1 : 1).slice(0, 5), as_of: new Date(now()).toISOString() } satisfies Schema['Overview'])
    if (p === '/groups') return json(state.groups)
    if (method === 'GET') {
      if (p === '/nodes') return json(paginate(state.nodes, f))
      if (p === '/users') return json(paginate(state.users, f))
      if (p === '/usage') return json(paginate(usage(), f))
      if (p === '/operations') return json(paginate(state.operations, { ...f, sort: 'created' }))
      if (p === '/alerts') return json(paginate(state.alerts, f))
      if (p.startsWith('/requests/')) { const result = state.requests[p.split('/')[2]]; return result ? json({ operation_id: result.operation_id }) : fail('NOT_FOUND', '没有找到该请求，可检查操作列表。', 404) }
      if (/^\/users\/[^/]+\/subscription$/.test(p)) { const u = state.users.find(u => u.id === p.split('/')[2]); return u ? json({ state: u.subscription_state, published_nodes: u.subscription_state === 'published' ? u.node_count : 0, total_nodes: u.node_count }) : fail('NOT_FOUND', '用户不存在。', 404) }
      for (const [prefix, items] of [['nodes', state.nodes], ['users', state.users], ['operations', state.operations]] as const) {
        if (p.startsWith(`/${prefix}/`)) { const item = items.find(x => x.id === p.split('/')[2]); return item ? json(item) : fail('NOT_FOUND', '记录不存在。', 404) }
      }
    }
    if (/^\/alerts\/[^/]+\/ack$/.test(p)) { const alert = state.alerts.find(a => a.id === p.split('/')[2]); if (!alert) return fail('NOT_FOUND', '告警不存在。', 404); alert.state = 'acknowledged'; save(); return json(alert) }
    if (/^\/users\/[^/]+\/subscription-link$/.test(p)) { const u = state.users.find(u => u.id === p.split('/')[2]); if (!u || u.subscription_state !== 'published') return fail('NOT_READY', '订阅尚未发布，当前不能交付。', 409); return json({ url: `https://subscription.example.invalid/s/DEMO-NOT-A-SECRET-${u.id}/surge.conf` }) }
    let user: Schema['User'] | undefined
    const isCreate = p === '/users' && method === 'POST', isDisable = /^\/users\/[^/]+\/disable$/.test(p) && method === 'POST'
    if (isCreate) {
      if (!body?.display_name?.trim() || !Array.isArray(body.group_ids) || !body.group_ids.length || !body.group_ids.every((id: string) => state.groups.some(g => g.id === id))) return fail('VALIDATION_ERROR', '请填写名称并选择有效节点组。')
      const targets = new Set(state.groups.filter(g => body.group_ids.includes(g.id)).flatMap(g => g.node_ids))
      user = { ...state.users[0], id: `usr-${crypto.randomUUID()}`, display_name: body.display_name.trim(), note: body.note ?? '', status: 'provisioning', revision: 1, group_ids: body.group_ids, node_count: targets.size, upload_bytes: '0', download_bytes: '0', subscription_state: 'pending', created_at: new Date(now()).toISOString(), quality: { ...state.users[0].quality, started_at: new Date(now()).toISOString(), has_historical_gap: false, gap_count: 0 } }
      state.users.unshift(user)
    } else if (isDisable) {
      user = state.users.find(u => u.id === p.split('/')[2]); if (!user) return fail('NOT_FOUND', '用户不存在。', 404)
      if (user.revision !== body?.revision) return fail('STALE_REVISION', '用户状态已变化，请刷新后重试。', 409)
      user.status = 'disabled'; user.revision++; user.subscription_state = 'revoke_pending'
    }
    if (user && key) {
      const targets = new Set(state.groups.filter(g => user!.group_ids.includes(g.id)).flatMap(g => g.node_ids))
      const op: Schema['Operation'] = { id: `op-${crypto.randomUUID()}`, kind: isCreate ? 'create_user' : 'disable_user', subject: user.display_name, user_id: user.id, state: 'queued', created_at: new Date(now()).toISOString(), targets: state.nodes.filter(n => targets.has(n.id)).map(n => ({ node_id: n.id, name: n.name, state: 'queued', message: '已接收，等待执行' })), subscription_state: user.subscription_state }
      if (isDisable) user.node_count = 0
      state.operations.unshift(op); state.requests[key] = { signature, operation_id: op.id }; save(); return json({ operation_id: op.id }, 202)
    }
    return fail('NOT_FOUND', '此演示接口尚未提供。', 404)
  }
}
let service: ReturnType<typeof createDemoService> | undefined
export async function demoFetch(request: Request) {
  service ??= createDemoService(sessionStorage)
  await new Promise(resolve => setTimeout(resolve, 160))
  if (request.signal.aborted) throw new DOMException('Aborted', 'AbortError')
  return service(request)
}
