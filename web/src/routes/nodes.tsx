import { useQuery } from '@tanstack/react-query'
import { Link, useParams } from '@tanstack/react-router'
import { ArrowUpRight, Server, Plus } from 'lucide-react'
import { NodeBatch, NodeSelectBox } from '@/components/business/node-batch'
import { NodeManagementPanel } from '@/components/business/node-management'
import { NodeTasks } from '@/components/business/node-tasks'
import { NodeProtocols } from '@/components/business/node-protocols'
import { NodeConfigSections } from '@/components/business/node-config'
import { api } from '@/api/client'
import { BackLink, DataState, FilterBar, KeyValue, PageHeader, Pagination, useFilters } from '@/components/business/common'
import { DataTable, type DisplayColumn } from '@/components/business/data-table'
import { StatusBadge } from '@/components/business/status'
import { accountingModeNames, formatBytes, formatRate, formatTime } from '@/lib/format'
import type { Node } from '@/api/types'

const nodeLink = (n: Node) => <Link to="/nodes/$nodeId" params={{ nodeId: n.id }} search={{}} className="entity-link"><div className="entity-icon"><Server size={18} /></div><div><strong>{n.name}</strong><small>{n.id.toUpperCase()} <span>· {n.location}</span></small></div></Link>

function ProtocolSummary({ node }: { node: Node }) {
  const current = node.inbounds.filter(i => i.active)
  return node.quality.sampled_at ? <div className="space-y-1"><div className="flex gap-1 flex-wrap">{[...new Set(current.map(i => i.protocol))].map(p => <span key={p} className="protocol-tag">{p.toUpperCase()}</span>)}</div><p className="text-xs text-muted-foreground">{current.length} 个入口 · {node.outbounds.filter(o => o.active).length} 个出站</p></div> : <span className="text-xs text-muted-foreground">等待容器快照同步</span>
}

function LiveRateSummary({ node }: { node: Node }) {
  if (node.status === 'offline') return <span className="text-xs text-muted-foreground">离线</span>
  return <div className="space-y-0.5 tabular-nums text-xs">
    <div>↓ <strong>{formatRate(node.download_bps)}</strong> · ↑ {formatRate(node.upload_bps)}</div>
    <div className="text-muted-foreground">{node.active_connections} 活跃连接</div>
  </div>
}

function BillingBar({ node }: { node: Node }) {
  const b = node.billing
  if (b.state === 'unconfigured' || b.quota_bytes === '0') {
    return <span className="text-xs text-muted-foreground">未设月配额</span>
  }
  const barWidth = Math.min(100, Math.max(0, b.used_percent))
  const tone = b.state === 'exceeded' ? 'bg-destructive' : b.state === 'warning' ? 'bg-amber-500' : 'bg-primary'
  return <div className="space-y-1.5 min-w-36">
    <div className="flex items-center justify-between gap-2 text-xs tabular-nums">
      <span>{formatBytes(b.used_bytes)} / {formatBytes(b.quota_bytes)}</span>
      <strong className={b.state === 'exceeded' ? 'text-destructive' : b.state === 'warning' ? 'text-amber-600 dark:text-amber-400' : ''}>{b.used_percent.toFixed(1)}%</strong>
    </div>
    <div className="h-1.5 w-full rounded-full bg-muted overflow-hidden">
      <div className={`h-full rounded-full transition-all ${tone}`} style={{ width: `${barWidth}%` }} />
    </div>
    <p className="text-[11px] text-muted-foreground">每月 {b.reset_day} 日重置 · {accountingModeNames[b.accounting_mode]}</p>
  </div>
}

const columns: DisplayColumn<Node>[] = [
  { id: 'select', label: '选择', render: n => <NodeSelectBox node={n} /> },
  { id: 'name', label: '节点', render: nodeLink },
  { id: 'status', label: '运行状态', render: n => <StatusBadge status={n.status} /> },
  { id: 'live', label: '实时速率', render: n => <LiveRateSummary node={n} /> },
  { id: 'billing', label: '月流量进度', render: n => <BillingBar node={n} /> },
  { id: 'protocol', label: '协议与分流', render: n => <ProtocolSummary node={n} /> },
  { id: 'users', label: '授权用户', render: n => <span className="tabular-nums">{n.users_count} 位</span> },
  { id: 'detail', label: '', render: n => <Link to="/nodes/$nodeId" params={{ nodeId: n.id }} search={{}} aria-label={`查看${n.name}`} className="icon-link"><ArrowUpRight size={16} /></Link> },
]

export function NodesPage() {
  const { filters, update } = useFilters(), query = useQuery({ queryKey: ['nodes', filters], queryFn: ({ signal }) => api.nodes(filters, signal), refetchInterval: 5_000 })
  return <>
    <PageHeader title="节点与协议" description="零 SSH 单容器管理：统一查看自研 sing-box 热重载配置、实时速率、月流量账单与协议入口。" action={<Link className="workspace-link bg-primary text-primary-foreground hover:bg-primary/90" to="/nodes/new" search={{}}><Plus size={16} />添加节点</Link>} />
    <section className="panel">
      <FilterBar placeholder="搜索节点名称或编号…" filters={filters} onChange={update} statuses={[['online', '在线'], ['degraded', '需关注'], ['offline', '失联']]} regions sort={false} />
      <NodeBatch nodes={query.error ? [] : query.data?.items ?? []} scope={JSON.stringify(filters)}>
        {query.isPending || query.error ? <DataState loading={query.isPending} error={query.error} retry={() => query.refetch()} /> : <>
          <div className="desktop-table"><DataTable data={query.data.items} columns={columns} rowId={n => n.id} /></div>
          <div className="mobile-cards">
            {query.data.items.map(n => <article className="mobile-record space-y-3" key={n.id}>
              <div className="flex items-center gap-3"><NodeSelectBox node={n} /><div className="flex min-w-0 flex-1 items-center justify-between gap-2">{nodeLink(n)}<StatusBadge status={n.status} /></div></div>
              <div className="record-metrics"><span>↓ <strong>{formatRate(n.download_bps)}</strong></span><span>↑ <strong>{formatRate(n.upload_bps)}</strong></span><span>授权 <strong>{n.users_count} 位</strong></span></div>
              <BillingBar node={n} />
              <ProtocolSummary node={n} />
            </article>)}
            {!query.data.items.length && <DataState />}
          </div>
          <Pagination data={query.data} onChange={update} />
        </>}
      </NodeBatch>
    </section>
    <p className="page-note">受管节点仅需运行单个自研 sing-box 容器，经 Tailscale 主动连接主控实现零停机热重载与实时流式上报。</p>
  </>
}

export function NodeDetail() {
  const { nodeId = '' } = useParams({ strict: false }), query = useQuery({ queryKey: ['node', nodeId], queryFn: ({ signal }) => api.node(nodeId, signal), refetchInterval: 5_000 })
  if (query.isPending || query.error) return <DataState loading={query.isPending} error={query.error} retry={() => query.refetch()} />
  const n = query.data
  const b = n.billing
  return <>
    <BackLink to="/nodes" label="返回节点列表" />
    <PageHeader title={n.name} description="在此通过内嵌控制通道管理 sing-box、配置月流量账单并查看协议入口。" action={<StatusBadge status={n.status} />} />
    <div className="detail-grid mb-5">
      <NodeManagementPanel nodeId={nodeId} />
      <section className="panel p-5 sm:p-6 space-y-5">
        <div>
          <h2>节点资料与实时状态</h2>
          <dl className="mt-4">
            <KeyValue label="节点 ID"><code>{n.id}</code></KeyValue>
            <KeyValue label="位置">{n.location}</KeyValue>
            <KeyValue label="控制通道">{n.agent_state === 'online' ? `单容器内嵌 Agent 在线${n.agent_version ? `（${n.agent_version}）` : ''}` : n.agent_state === 'offline' ? '内嵌 Agent 离线（等待容器重连）' : '等待容器首次注册'}</KeyValue>
            {n.agent_last_seen && <KeyValue label="最近心跳">{formatTime(n.agent_last_seen, true)}</KeyValue>}
            <KeyValue label="实时吞吐"><span className="tabular-nums">↓ {formatRate(n.download_bps)} · ↑ {formatRate(n.upload_bps)}（{n.active_connections} 活跃连接）</span></KeyValue>
            <KeyValue label="Tailscale 地址"><code>{n.management_address}</code></KeyValue>
            <KeyValue label="公网端点"><code>{n.public_host || '—'}</code></KeyValue>
            <KeyValue label="内核版本">sing-box {n.version}</KeyValue>
            <KeyValue label="已应用版本">revision {n.revision}</KeyValue>
            <KeyValue label="配置同步">{n.config_sync_state === 'synced' ? '已同步' : n.config_sync_state === 'pending_restart' ? '外部修改待热重载' : n.config_sync_state === 'pending_sync' ? '正在同步外部修改' : n.config_sync_state === 'unknown' ? '等待首次同步' : '需处理'}</KeyValue>
            {n.config_sync_error && <KeyValue label="同步错误"><code>{n.config_sync_error}</code></KeyValue>}
            <KeyValue label="授权用户">{n.users_count} 位</KeyValue>
          </dl>
        </div>
        <div className="border-t pt-4">
          <div className="flex items-center justify-between gap-2 mb-3">
            <h3 className="text-sm font-medium">月流量计费周期</h3>
            <Link to="/usage" search={{ node: n.id }} className="text-xs text-primary inline-flex items-center gap-1">流量明细 <ArrowUpRight size={13} /></Link>
          </div>
          {b.state === 'unconfigured' || b.quota_bytes === '0' ? (
            <p className="text-xs text-muted-foreground">尚未设置月流量配额，可在左侧点击“编辑信息”设置每月流量上限与账单重置日。</p>
          ) : (
            <div className="space-y-2">
              <BillingBar node={n} />
              <p className="text-xs text-muted-foreground tabular-nums">本周期：{b.cycle_from} 至 {b.cycle_to} · 上传 {formatBytes(b.upload_bytes)} / 下载 {formatBytes(b.download_bytes)}</p>
            </div>
          )}
        </div>
      </section>
    </div>
    <NodeProtocols key={nodeId} node={n} />
    <NodeConfigSections key={`config-${nodeId}`} nodeId={nodeId} />
    <NodeTasks nodeId={nodeId} />
  </>
}
