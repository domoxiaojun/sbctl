import type { Schema } from '../api/types'
type NodeState = { nodes: Schema['Node'][]; groups: Schema['Group'][] }
export function createNodeManagementDemo(state: () => NodeState, storage?: Pick<Storage, 'getItem' | 'setItem'>) {
  const jobs = new Map<string, Schema['NodeJob']>(), configurations = new Map<string, Schema['NodeConfiguration']>(), requests = new Map<string, { input: string; result: unknown }>()
  const storageKey = 'sbctl-demo-node-management-v1'
  try { const saved = JSON.parse(storage?.getItem(storageKey) ?? '{}');for (const [k,v] of saved.jobs ?? []) jobs.set(k,v);for (const [k,v] of saved.configurations ?? []) configurations.set(k,v);for (const [k,v] of saved.requests ?? []) requests.set(k,v) } catch { /* synthetic state may reset */ }
  const json = (body: unknown, status = 200) => { try { storage?.setItem(storageKey, JSON.stringify({jobs:[...jobs], configurations:[...configurations],requests:[...requests]})) } catch { /* synthetic storage unavailable */ }; return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }) }
  const failure = (message: string) => json({ code: 'NODE_INVALID', message }, 409)
  const config = (id: string): Schema['NodeConfiguration'] | null => {
    if (configurations.has(id)) return configurations.get(id)!
    const n = state().nodes.find(n => n.id === id); if (!n) return null
    const group = state().groups.find(g => g.node_ids.includes(id)) ?? state().groups[0]
    return { version: 0, draft: { id, name: n.name, region: n.region, location: n.location, public_host: n.public_host, config_dir: '/etc/sing-box', container: 'sing-box', group_id: group.id, group_name: group.name, connection: { address: n.management_address, port: 22, publish_user: 'sbctl-publish', stats_user: 'sbctl-stats', publish_key: 'publish_ed25519', stats_key: 'stats_ed25519', version: n.version, host_key: '' } }, binding: { container: 'sing-box', image: 'example.invalid/sing-box:demo', engine_build_id: 'a'.repeat(64) }, checked_at: null, last_check: null }
  }
  return async (req: Request, path: string): Promise<Response | null> => {
    if (!(path.startsWith('/node-') || /^\/nodes\/[^/]+\/(config|metadata)$/.test(path))) return null
    if (path === '/node-discovery') return json({ available: true, sampled_at: new Date().toISOString(), message: '演示主机发现', hosts: [{ name: '香港新节点', address: '100.70.0.99', online: true, os: 'linux', node_id: '' }, { name: '暂离线主机', address: '100.70.0.98', online: false, os: 'linux', node_id: '' }] } satisfies Schema['NodeDiscovery'])
    if (path === '/node-onboarding') return json({ keys: { publish: '演示公钥占位，请勿用于部署', stats: '演示公钥占位，请勿用于部署' }, instructions: ['安装固定 helper', '配置主控公钥', '核对 SSH 主机公钥'], release: 'demo' } satisfies Schema['NodeOnboarding'])
    const body = req.method === 'GET' ? null : await req.json().catch(() => null)
    const key = req.headers.get('Idempotency-Key'), input = JSON.stringify({path,body})
    if (key && requests.has(key)) { const old = requests.get(key)!; return old.input === input ? json(old.result, path === '/node-jobs' ? 202 : 200) : failure('请求标识内容不一致') }
    if (/^\/nodes\/[^/]+\/config$/.test(path)) { const value = config(path.split('/')[2]);return value ? json(value) : failure('节点不存在') }
    if (/^\/nodes\/[^/]+\/metadata$/.test(path)) {
      const id = path.split('/')[2], saved = config(id);if (!saved || saved.version !== body.version) return failure('节点配置已变化')
      const draft = body.draft as Schema['NodeDraft'];const n = state().nodes.find(n => n.id === id)!
      Object.assign(n, { name: draft.name, region: draft.region, location: draft.location });saved.draft = draft;saved.version++;configurations.set(id,saved)
      const result = { saved: true, version: saved.version };if (key) requests.set(key,{input,result});return json(result)
    }
    if (path === '/node-jobs' && req.method === 'POST') {
      const submission = body as Schema['NodeJobInput'], d = submission.draft, id = `njob_${crypto.randomUUID().replaceAll('-','')}`, stamp = new Date().toISOString()
      const result: Record<string, unknown> = submission.kind === 'inspect' ? { container: d.container || 'sing-box', config_dir: d.config_dir, image: 'example.invalid/sing-box:latest', engine_build_id: 'a'.repeat(64), can_adopt: true, preserved_users: 1, version: 'sing-box 1.12.9', impact: '演示接管：保留容器名和镜像；必要时短暂重连。', endpoints: [{protocol:'anytls',port:5443},{protocol:'tuic',port:4443}] } : submission.kind === 'check' ? { ready: true, checks: [{name:'运行绑定',ok:true,message:'演示容器和配置一致'}] } : { node_id: d.id, container: submission.desired_container || d.container || 'sing-box', config_dir:d.config_dir, image:submission.desired_image || 'example.invalid/sing-box:latest', ready:true }
      if (submission.kind === 'configure') {
        configurations.set(d.id,{version:submission.expected_version+1,draft:d,binding:result,checked_at:stamp,last_check:result})
        const existing = state().nodes.find(n => n.id === d.id)
        if (existing) Object.assign(existing,{name:d.name,region:d.region,location:d.location,management_address:d.connection.address,public_host:d.public_host})
        else state().nodes.unshift({...state().nodes[0],id:d.id,name:d.name,region:d.region,location:d.location,management_address:d.connection.address,public_host:d.public_host,users_count:0,upload_bytes:'0',download_bytes:'0',status:'online'})
      }
      if (submission.kind === 'check') { const saved = config(d.id);if (saved) configurations.set(d.id,{...saved,checked_at:stamp,last_check:result}) }
      if (submission.kind === 'configure') d.container = String(result.container)
      jobs.set(id,{id,kind:submission.kind,node_id:d.id,state:'completed',result,error_code:null,message:'演示操作已完成',created_at:stamp,updated_at:stamp})
      const response={job_id:id};if(key)requests.set(key,{input,result:response});return json(response,202)
    }
    if (/^\/node-jobs\/[^/]+$/.test(path)) { const job=jobs.get(path.split('/')[2]);return job?json(job):failure('节点任务不存在') }
    return failure('演示操作不可用')
  }
}
