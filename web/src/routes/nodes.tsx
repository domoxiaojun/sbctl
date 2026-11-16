import { useQuery } from '@tanstack/react-query'
import { Link, useParams } from '@tanstack/react-router'
import { ArrowUpRight, Server, Plus } from 'lucide-react'
import { NodeManagementPanel } from '@/components/business/node-management'
import { NodeTasks } from '@/components/business/node-tasks'
import { NodeProtocols } from '@/components/business/node-protocols'
import { NodeConfigSections } from '@/components/business/node-config'
import { api } from '@/api/client'
import { BackLink, DataState, FilterBar, KeyValue, PageHeader, Pagination, useFilters } from '@/components/business/common'
import { DataTable, type DisplayColumn } from '@/components/business/data-table'
import { StatusBadge } from '@/components/business/status'
import type { Node } from '@/api/types'
const nodeLink = (n: Node) => <Link to="/nodes/$nodeId" params={{ nodeId: n.id }} search={{}} className="entity-link"><div className="entity-icon"><Server size={18} /></div><div><strong>{n.name}</strong><small>{n.id.toUpperCase()} <span>· {n.location}</span></small></div></Link>
function ProtocolSummary({ node }: { node: Node }) {
  const current = node.inbounds.filter(i => i.active)
  return node.quality.sampled_at ? <div className="space-y-1"><div className="flex gap-1 flex-wrap">{[...new Set(current.map(i => i.protocol))].map(p => <span key={p} className="protocol-tag">{p.toUpperCase()}</span>)}</div><p className="text-xs text-muted-foreground">{current.length} 个入口 · {node.quality.freshness === 'current' ? '最近快照' : '快照待更新'}</p></div> : <span className="text-xs text-muted-foreground">等待入口同步</span>
}
const columns: DisplayColumn<Node>[] = [
  { id: 'name', label: '节点', render: nodeLink },
  { id: 'status', label: '运行状态', render: n => <StatusBadge status={n.status} /> },
  { id: 'protocol', label: '协议入口', render: n => <ProtocolSummary node={n} /> },
  { id: 'version', label: 'sing-box 版本', render: n => <code>{n.version || '—'}</code> },
  { id: 'users', label: '授权用户', render: n => <span className="tabular-nums">{n.users_count} 位</span> },
  { id: 'detail', label: '', render: n => <Link to="/nodes/$nodeId" params={{ nodeId: n.id }} search={{}} aria-label={`查看${n.name}`} className="icon-link"><ArrowUpRight size={16} /></Link> },
]
export function NodesPage() {
  const { filters, update } = useFilters(), query = useQuery({ queryKey: ['nodes', filters], queryFn: ({ signal }) => api.nodes(filters, signal) })
  return <><PageHeader title="节点与协议" description="选择节点，统一查看 sing-box 运行配置、协议入口和节点检查。" action={<Link className="workspace-link bg-primary text-primary-foreground hover:bg-primary/90" to="/nodes/new" search={{}}><Plus size={16} />添加节点</Link>} /><section className="panel"><FilterBar placeholder="搜索节点名称或编号…" filters={filters} onChange={update} statuses={[[ 'online', '在线' ], ['degraded', '需关注'], ['offline', '失联']]} regions sort={false} />{query.isPending || query.error ? <DataState loading={query.isPending} error={query.error} retry={() => query.refetch()} /> : <><div className="desktop-table"><DataTable data={query.data.items} columns={columns} rowId={n => n.id} /></div><div className="mobile-cards">{query.data.items.map(n => <article className="mobile-record" key={n.id}><div className="flex items-center justify-between gap-2">{nodeLink(n)}<StatusBadge status={n.status} /></div><div className="record-metrics"><span>sing-box <strong>{n.version}</strong></span><span>授权 <strong>{n.users_count} 位</strong></span></div><ProtocolSummary node={n} /></article>)}{!query.data.items.length && <DataState />}</div><Pagination data={query.data} onChange={update} /></>}</section><p className="page-note">协议入口来自最近节点快照。按日用量、排行和历史记录统一在流量统计查看。</p></>
}
export function NodeDetail() {
  const { nodeId = '' } = useParams({ strict: false }), query = useQuery({ queryKey: ['node', nodeId], queryFn: ({ signal }) => api.node(nodeId, signal), refetchInterval: 15_000 })
  if (query.isPending || query.error) return <DataState loading={query.isPending} error={query.error} retry={() => query.refetch()} />
  const n = query.data
  return <><BackLink to="/nodes" label="返回节点列表" /><PageHeader title={n.name} description="在此管理 sing-box、检查节点并查看协议入口。" action={<StatusBadge status={n.status} />} /><div className="detail-grid mb-5"><NodeManagementPanel nodeId={nodeId} /><section className="panel p-5 sm:p-6"><h2>节点资料</h2><dl className="mt-5"><KeyValue label="节点 ID"><code>{n.id}</code></KeyValue><KeyValue label="位置">{n.location}</KeyValue><KeyValue label="管理地址"><code>{n.management_address}</code></KeyValue><KeyValue label="公网端点"><code>{n.public_host}</code></KeyValue><KeyValue label="内核版本">sing-box {n.version}</KeyValue><KeyValue label="已应用版本">revision {n.revision}</KeyValue><KeyValue label="授权用户">{n.users_count} 位</KeyValue></dl></section></div><NodeProtocols key={nodeId} node={n} /><NodeConfigSections key={`config-${nodeId}`} nodeId={nodeId} /><NodeTasks nodeId={nodeId} /></>
}
