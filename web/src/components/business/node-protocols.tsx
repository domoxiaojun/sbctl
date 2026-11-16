import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { ArrowUpRight, Search } from 'lucide-react'
import type { Node } from '@/api/types'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { DataTable, type DisplayColumn } from './data-table'
import { FilterSelect, Pagination } from './common'
import { formatTime } from '@/lib/format'

type Inbound = Node['inbounds'][number]
const columns: DisplayColumn<Inbound>[] = [
  { id: 'tag', label: '入口标签', render: i => <code className="break-all">{i.tag}</code> },
  { id: 'protocol', label: '协议', render: i => <span className="protocol-tag">{i.protocol.toUpperCase()}</span> },
  { id: 'port', label: '监听端口', render: i => <span className="numeric">{i.port || '—'}</span> },
]

export function NodeProtocols({ node }: { node: Node }) {
  const [q, setQ] = useState(''), [protocol, setProtocol] = useState('all'), [selectedPage, setPage] = useState(1)
  const current = node.inbounds.filter(i => i.active)
  const protocols = [...new Set(current.map(i => i.protocol))].sort()
  const rows = current.filter(i => (protocol === 'all' || i.protocol === protocol) && `${i.tag} ${i.protocol} ${i.port}`.toLowerCase().includes(q.trim().toLowerCase())).sort((a, b) => a.protocol.localeCompare(b.protocol) || a.port - b.port || a.tag.localeCompare(b.tag))
  const page = Math.min(selectedPage, Math.max(1, Math.ceil(rows.length / 10)))
  const items = rows.slice((page - 1) * 10, page * 10)
  const known = !!node.quality.sampled_at
  return <section className="panel overflow-hidden" aria-labelledby="node-protocols-heading">
    <div className="p-5 space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3"><div><h2 id="node-protocols-heading">协议入口</h2><p className="text-sm text-muted-foreground mt-2">{known ? `最近快照包含 ${current.length} 个当前入口、${protocols.length} 种协议。` : '尚未收到入口快照，协议清单待同步。'}</p></div><Link to="/usage" search={{ node: node.id, scope: 'inbounds' }} className="workspace-link">查看节点流量<ArrowUpRight size={16} /></Link></div>
      <p className="text-xs text-muted-foreground">最近同步 {formatTime(node.quality.synced_at, true)}{node.quality.freshness !== 'current' && ' · 快照已过期或新鲜度未知，请结合节点检查确认配置。'}</p>
      <div className="flex flex-wrap gap-2"><div className="relative grow basis-48"><Search className="absolute left-3 top-3.5 text-muted-foreground" size={16} /><Input className="pl-9 min-h-11" aria-label="搜索协议入口" placeholder="搜索入口标签、协议或端口" value={q} onChange={e => { setQ(e.target.value); setPage(1) }} /></div><div className="grow sm:grow-0 basis-40"><FilterSelect label="入口协议类型" value={protocol} onChange={v => { setProtocol(v); setPage(1) }} options={[{ value: 'all', label: '全部协议' }, ...protocols.map(p => ({ value: p, label: p.toUpperCase() }))]} /></div>{(q || protocol !== 'all') && <Button variant="ghost" className="min-h-11" onClick={() => { setQ(''); setProtocol('all'); setPage(1) }}>清除筛选</Button>}</div>
    </div>
    {rows.length ? <><div className="desktop-table"><DataTable data={items} columns={columns} rowId={i => i.tag} /></div><div className="mobile-cards">{items.map(i => <article className="mobile-record" key={i.tag}><code className="break-all font-medium">{i.tag}</code><div className="record-metrics"><span className="protocol-tag">{i.protocol.toUpperCase()}</span><span>监听端口 <strong>{i.port || '—'}</strong></span></div></article>)}</div><Pagination data={{ page, page_size: 10, total: rows.length }} onChange={next => setPage(next.page ?? 1)} /></> : <p role="status" className="p-5 text-sm text-muted-foreground">{!known ? '等待节点同步后显示入口。' : !current.length ? '最近快照没有当前协议入口。' : '没有匹配的协议入口，请调整筛选条件。'}</p>}
    <div className="border-t p-5 text-xs text-muted-foreground space-y-2"><p>此处展示协议配置清单。入口配置在节点服务器维护，变更后由同步快照更新；已移除入口的历史用量在流量统计查询。</p><p>sbctl 用户授权支持：{node.protocols.join('、') || '尚未配置'}。用户的节点组权限与订阅统一在<Link to="/users" search={{}} className="underline underline-offset-4">用户与授权</Link>管理。</p></div>
  </section>
}
