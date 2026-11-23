import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { ArrowUpRight, Plus, RefreshCw, Search } from 'lucide-react'
import type { Node, Schema } from '@/api/types'
import { api } from '@/api/client'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { DataTable, type DisplayColumn } from './data-table'
import { FilterSelect, Pagination } from './common'
import { formatTime } from '@/lib/format'
import { useNodeCommand } from '@/lib/node-jobs'
import { ProtocolEditor } from './protocol-editor'

type Item = Schema['ProtocolItem']
export function NodeProtocols({ node }: { node: Node }) {
  const [q, setQ] = useState(''), [protocol, setProtocol] = useState('all'), [selectedPage, setPage] = useState(1), [key, setKey] = useState(() => crypto.randomUUID())
  const [editor, setEditor] = useState<{ action: Schema['ProtocolChange']['action']; item?: Item } | null>(null)
  const config = useQuery({ queryKey: ['node-config', node.id], queryFn: ({ signal }) => api.nodeConfig(node.id, signal) }), { submit, job, busy } = useNodeCommand(node.id)
  const catalog = config.data?.protocol_catalog
  const items: Item[] = catalog?.items ?? node.inbounds.filter(i => i.active).map(i => ({ tag: i.tag, protocol: i.protocol, port: i.port, listen: '', enabled: true, managed: false, user_count: 0, tls: false, method: '', target_host: '', target_port: 0 }))
  const current = items.filter(i => i.enabled), protocols = [...new Set(items.map(i => i.protocol))].sort()
  const rows = items.filter(i => (protocol === 'all' || i.protocol === protocol) && `${i.tag} ${i.protocol} ${i.port}`.toLowerCase().includes(q.trim().toLowerCase())).sort((a, b) => a.protocol.localeCompare(b.protocol) || a.port - b.port || a.tag.localeCompare(b.tag))
  const page = Math.min(selectedPage, Math.max(1, Math.ceil(rows.length / 10))), visible = rows.slice((page - 1) * 10, page * 10)
  const known = !!node.quality.sampled_at || !!catalog
  const status = (i: Item) => i.retirement ? (i.retirement.state === 'verified' ? '撤销已验证，待退役' : i.retirement.state === 'applied' ? '撤销已应用，待验证' : '正在撤销入口授权') : i.enabled ? '已启用' : '已停用'
  const actions = (i: Item) => <div className="flex flex-wrap gap-2"><Button variant="outline" disabled={!catalog || busy || !!i.retirement} onClick={() => setEditor({ action: 'edit', item: i })}>编辑</Button><Button variant="outline" disabled={!catalog || busy || !!i.retirement || i.referenced} onClick={() => setEditor({ action: i.enabled ? 'disable' : 'enable', item: i })}>{i.enabled ? '停用' : '启用'}</Button><Button variant="ghost" className="text-destructive" disabled={!catalog || busy || !!i.retirement || i.referenced} onClick={() => setEditor({ action: 'delete', item: i })}>删除</Button></div>
  const columns: DisplayColumn<Item>[] = [
    { id: 'tag', label: '入口标签', render: i => <code className="break-all">{i.tag}</code> },
    { id: 'protocol', label: '协议', render: i => <span className="protocol-tag">{i.protocol.toUpperCase()}</span> },
    { id: 'port', label: '监听', render: i => <span className="numeric break-all">{i.listen && `${i.listen} : `}{i.port || '—'}</span> },
    { id: 'status', label: '状态', render: i => <div><span className="protocol-tag">{status(i)}</span>{i.retirement && <p className="text-xs text-muted-foreground">{formatTime(i.retirement.created_at)} · {i.retirement.message}</p>}{i.managed && !i.retirement && <p className="text-xs text-muted-foreground">{i.probe_state === 'passed' ? '当前配置按需拨测已通过' : '当前配置未执行按需拨测'}</p>}</div> },
    { id: 'actions', label: '管理操作', render: actions },
  ]
  return <section className="panel overflow-hidden" aria-labelledby="node-protocols-heading">
    <div className="p-5 space-y-4"><div className="flex flex-wrap items-start justify-between gap-3"><div><h2 id="node-protocols-heading">协议入口</h2><p className="text-sm text-muted-foreground mt-2">{known ? `最近${catalog ? '配置' : '快照'}包含 ${current.length} 个当前入口、${protocols.length} 种协议。` : '尚未收到入口快照，协议清单待同步。'}{catalog && `已停用 ${items.length - current.length} 个。`}</p></div><div className="flex flex-wrap gap-2"><Button variant="outline" disabled={!config.data || busy} onClick={() => submit.mutate({ key, input: { kind: 'protocols', draft: config.data!.draft, expected_version: config.data!.version, inspection_id: '' } }, { onSuccess: () => setKey(crypto.randomUUID()) })}><RefreshCw size={16} />{busy ? '任务执行中…' : '读取协议配置'}</Button><Button variant="outline" disabled={!config.data || busy || !current.some(i => i.managed)} onClick={() => submit.mutate({ key, input: { kind: 'verify_inbounds', draft: config.data!.draft, expected_version: config.data!.version, inspection_id: '' } }, { onSuccess: () => setKey(crypto.randomUUID()) })}>节点侧拨测</Button><Button disabled={!catalog || busy} onClick={() => setEditor({ action: 'create' })}><Plus size={16} />新增协议入口</Button><Link to="/usage" search={{ node: node.id, scope: 'inbounds' }} className="workspace-link">查看节点流量<ArrowUpRight size={16} /></Link></div></div>
      <p className="text-xs text-muted-foreground">{catalog ? `配置读取 ${formatTime(catalog.checked_at, true)}` : `最近同步 ${formatTime(node.quality.synced_at, true)} · 先读取协议配置，即可新增、编辑或启停入口。`}{!catalog && node.quality.freshness !== 'current' && ' 快照已过期或新鲜度未知。'}</p>
      {(submit.error || config.error) && <p role="alert" className="field-error">{submit.error?.message ?? config.error?.message}</p>}{job.data && ['protocols', 'protocol_change', 'verify_inbounds'].includes(job.data.kind) && <p role="status" className={`text-sm ${job.data.state === 'failed' ? 'text-destructive' : 'text-muted-foreground'}`}>{job.data.state === 'completed' && job.data.kind !== 'verify_inbounds' ? '协议任务已完成，配置清单已更新。' : job.data.message || '任务已保存，正在执行；可在下方查看进度。'}</p>}
      <div className="flex flex-wrap gap-2"><div className="relative grow basis-48"><Search className="absolute left-3 top-3.5 text-muted-foreground" size={16} /><Input className="pl-9 min-h-11" aria-label="搜索协议入口" placeholder="搜索入口标签、协议或端口" value={q} onChange={e => { setQ(e.target.value); setPage(1) }} /></div><div className="grow sm:grow-0 basis-40"><FilterSelect label="入口协议类型" value={protocol} onChange={v => { setProtocol(v); setPage(1) }} options={[{ value: 'all', label: '全部协议' }, ...protocols.map(p => ({ value: p, label: p.toUpperCase() }))]} /></div>{(q || protocol !== 'all') && <Button variant="ghost" className="min-h-11" onClick={() => { setQ(''); setProtocol('all'); setPage(1) }}>清除筛选</Button>}</div>
    </div>
    {rows.length ? <><div className="desktop-table"><DataTable data={visible} columns={columns} rowId={i => i.inbound_id ?? i.tag} /></div><div className="mobile-cards">{visible.map(i => <article className="mobile-record" key={i.inbound_id ?? i.tag}><div className="flex flex-wrap justify-between gap-2"><code className="break-all font-medium">{i.tag}</code><span className="protocol-tag">{status(i)}</span></div><div className="record-metrics"><span className="protocol-tag">{i.protocol.toUpperCase()}</span><span>监听端口 <strong>{i.port || '—'}</strong></span></div>{actions(i)}</article>)}</div><Pagination data={{ page, page_size: 10, total: rows.length }} onChange={next => setPage(next.page ?? 1)} /></> : <p role="status" className="p-5 text-sm text-muted-foreground">{!known ? '等待节点同步后显示入口。' : !items.length ? '最近配置没有协议入口。' : '没有匹配的协议入口，请调整筛选条件。'}</p>}
    <div className="border-t p-5 text-xs text-muted-foreground space-y-2"><p>支持按现有模板新增 AnyTLS、TUIC、VMess、VLESS、Shadowsocks 和转发入口。停用保留配置，删除保留历史流量。入口的稳定 ID 与标签分离，删除后重新创建会生成新身份。</p><p>「节点侧拨测」为独立按需诊断操作，不阻塞授权或订阅，也不在配置中创建常驻系统探针账号。sbctl 用户授权支持：{node.protocols.join('、') || '尚未配置'}；独立用户授权和订阅在<Link to="/users" search={{}} className="underline underline-offset-4">用户与授权</Link>管理。</p></div>
    {editor && config.data?.protocol_catalog && <ProtocolEditor config={config.data} action={editor.action} item={editor.item} onClose={() => setEditor(null)} />}
  </section>
}
