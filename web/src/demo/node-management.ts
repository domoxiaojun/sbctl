import type { Schema } from '../api/types'
type NodeState = { nodes: Schema['Node'][]; groups: Schema['Group'][] }
export function createNodeManagementDemo(state: () => NodeState, storage?: Pick<Storage, 'getItem' | 'setItem'>) {
  const jobs = new Map<string, Schema['NodeJob']>(), configurations = new Map<string, Schema['NodeConfiguration']>(), requests = new Map<string, { input: string; result: unknown }>(), sections = new Map<string, { hash: string; sections: Schema['NodeConfigView']['sections'] }>()
  const storageKey = 'sbctl-demo-node-management-v1'
  try { const saved = JSON.parse(storage?.getItem(storageKey) ?? '{}');for (const [k,v] of saved.jobs ?? []) jobs.set(k,v);for (const [k,v] of saved.configurations ?? []) configurations.set(k,v);for (const [k,v] of saved.requests ?? []) requests.set(k,v);for (const [k,v] of saved.sections ?? []) sections.set(k,v) } catch { /* synthetic state may reset */ }
  const json = (body: unknown, status = 200) => { try { storage?.setItem(storageKey, JSON.stringify({jobs:[...jobs], configurations:[...configurations],requests:[...requests],sections:[...sections]})) } catch { /* synthetic storage unavailable */ }; return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }) }
  const failure = (message: string) => json({ code: 'NODE_INVALID', message }, 409)
  const config = (id: string): Schema['NodeConfiguration'] | null => {
    if (configurations.has(id)) return configurations.get(id)!
    const n = state().nodes.find(n => n.id === id); if (!n) return null
    const group = state().groups.find(g => g.node_ids.includes(id)) ?? state().groups[0]
    return { protocol_catalog: null, version: 0, draft: { id, name: n.name, region: n.region, location: n.location, public_host: n.public_host, config_dir: '/etc/sing-box', container: 'sing-box', group_id: group.id, group_name: group.name, connection: { address: n.management_address, port: 22, publish_user: 'sbctl-publish', stats_user: 'sbctl-stats', publish_key: 'publish_ed25519', stats_key: 'stats_ed25519', version: n.version, host_key: '' } }, binding: { container: 'sing-box', image: 'example.invalid/sing-box:demo', engine_build_id: 'a'.repeat(64) }, checked_at: null, last_check: null }
  }
  const editable = (id: string) => {
    if (!sections.has(id)) sections.set(id, { hash: 'c'.repeat(64), sections: { log: { level: 'info', timestamp: true }, dns: { servers: [{ type: 'local', tag: 'local' }] }, ntp: {}, route: { rules: [{ inbound: ['anytls-in', 'tuic-in'], action: 'sniff' }], final: 'direct' } } })
    return sections.get(id)!
  }
  return async (req: Request, path: string): Promise<Response | null> => {
    if (!(path.startsWith('/node-') || /^\/nodes\/[^/]+\/(config|metadata|jobs)$/.test(path))) return null
    if (path === '/node-discovery') return json({ available: true, sampled_at: new Date().toISOString(), message: '演示主机发现', hosts: [{ name: '香港新节点', address: '100.70.0.99', online: true, os: 'linux', node_id: '' }, { name: '暂离线主机', address: '100.70.0.98', online: false, os: 'linux', node_id: '' }] } satisfies Schema['NodeDiscovery'])
    if (path === '/node-onboarding') return json({ keys: { publish: '演示公钥占位，请勿用于部署', stats: '演示公钥占位，请勿用于部署' }, instructions: ['安装固定 helper', '配置主控公钥', '核对 SSH 主机公钥'], release: 'demo' } satisfies Schema['NodeOnboarding'])
    const body = req.method === 'GET' ? null : await req.json().catch(() => null)
    const key = req.headers.get('Idempotency-Key'), input = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(JSON.stringify({path,body}))))).map(v => v.toString(16).padStart(2, '0')).join('')
    if (key && requests.has(key)) { const old = requests.get(key)!; return old.input === input ? json(old.result, path === '/node-jobs' ? 202 : 200) : failure('请求标识内容不一致') }
    if (/^\/nodes\/[^/]+\/jobs$/.test(path)) return json([...jobs.values()].filter(j => j.node_id === path.split('/')[2]).reverse().slice(0,20).map(j=>({...j,result:null})))
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
      if (submission.kind === 'config' || submission.kind === 'config_change') {
        const saved=config(d.id), n=state().nodes.find(n=>n.id===d.id)
        if(!saved || !n)return failure('节点不存在')
        const current=editable(d.id)
        if(submission.kind==='config_change') {
          const change=submission.config_change!
          if(current.hash!==change.base_hash || saved.version!==submission.expected_version)return failure('配置已变化，请重新读取后操作')
          if(JSON.stringify(change.content).includes('demo-preflight-failure'))return failure('候选配置预检失败')
          current.sections={...current.sections,[change.section]:change.content}
          current.hash=crypto.randomUUID().replaceAll('-','').repeat(2)
        } else {
          for (const key of Object.keys(result)) delete result[key]
          Object.assign(result,{node_id:d.id,hash:current.hash,checked_at:stamp,sections:current.sections,outbounds:[{tag:'direct',type:'direct',server:'',server_port:0}],inbound_tags:n.inbounds.filter(i=>i.active).map(i=>i.tag)} satisfies Schema['NodeConfigView'])
        }
      }
      if (['protocols','protocol_change','config_change','restart'].includes(submission.kind)) {
        const saved=config(d.id), n=state().nodes.find(n=>n.id===d.id)
        if(!saved || !n)return failure('节点不存在')
        const catalog: Schema['ProtocolCatalog']=saved.protocol_catalog ?? {node_id:d.id,hash:'b'.repeat(64),checked_at:stamp,items:n.inbounds.filter(i=>i.active).map(i=>({tag:i.tag,protocol:i.protocol,listen:'::',port:i.port,enabled:true,managed:['anytls','tuic'].includes(i.protocol),user_count:1,tls:i.protocol!=='direct',method:i.protocol==='shadowsocks'?'aes-128-gcm':'',target_host:i.protocol==='direct'?'example.test':'',target_port:i.protocol==='direct'?443:0}))}
        if(submission.kind==='protocol_change') {
          const change=submission.protocol_change!, existing=catalog.items.find(i=>i.tag===change.tag)
          if(catalog.hash!==change.base_hash || saved.version!==submission.expected_version)return failure('协议配置已变化，请重新读取')
          if(existing?.referenced && ['disable','delete'].includes(change.action))return failure('入口仍被路由规则引用')
          if(change.action==='create') {
            if(existing)return failure('入口标签已经使用')
            const source=catalog.items.find(i=>i.tag===change.source_tag);if(!source)return failure('配置模板不存在')
            const inbound_id='inb_'+crypto.randomUUID().replaceAll('-','')
            catalog.items.push({...source,inbound_id,tag:change.tag,listen:change.listen!,port:change.port!,managed:!!change.subscription_host,target_host:change.target_host??'',target_port:change.target_port??0})
            n.inbounds.push({inbound_id,tag:change.tag,protocol:source.protocol,port:change.port!,active:true,upload_bytes:'0',download_bytes:'0',started_at:stamp})
          } else if(!existing)return failure('入口不存在')
          else if(change.action==='edit'){existing.listen=change.listen!;existing.port=change.port!;existing.target_host=change.target_host??'';existing.target_port=change.target_port??0;const counter=n.inbounds.find(i=>existing.inbound_id ? i.inbound_id===existing.inbound_id : i.active && i.tag===change.tag);if(counter){counter.port=change.port!;counter.tag=change.new_tag??change.tag}existing.tag=change.new_tag??change.tag;existing.managed=existing.managed||!!change.subscription_host}
          else {existing.enabled=change.action==='enable';const counter=n.inbounds.find(i=>existing.inbound_id ? i.inbound_id===existing.inbound_id : i.tag===change.tag && !i.inbound_id);if(counter)counter.active=existing.enabled;if(change.action==='delete')catalog.items=catalog.items.filter(i=>i.tag!==change.tag)}
          catalog.hash=crypto.randomUUID().replaceAll('-','').repeat(2)
        }
        if(submission.kind!=='protocols')saved.version++
        catalog.checked_at=stamp;saved.protocol_catalog=catalog;configurations.set(d.id,saved)
        Object.assign(result,submission.kind==='protocols'?catalog:{node_id:d.id,ready:true,catalog,template_hash:'b'.repeat(64),endpoints:[]})
        n.total_inbounds=n.inbounds.filter(i=>i.active).length
      }
      if (submission.kind === 'verify_inbounds') {
        const saved=config(d.id)
        if(!saved)return failure('节点不存在')
        for(const item of saved.protocol_catalog?.items??[])if(item.managed&&item.enabled)item.probe_state='passed'
        configurations.set(d.id,saved)
      }
      if (submission.kind === 'configure') {
        configurations.set(d.id,{protocol_catalog:null,version:submission.expected_version+1,draft:d,binding:result,checked_at:stamp,last_check:result})
        const existing = state().nodes.find(n => n.id === d.id)
        if (existing) Object.assign(existing,{name:d.name,region:d.region,location:d.location,management_address:d.connection.address,public_host:d.public_host})
        else state().nodes.unshift({...state().nodes[0],id:d.id,name:d.name,region:d.region,location:d.location,management_address:d.connection.address,public_host:d.public_host,users_count:0,status:'online'})
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
