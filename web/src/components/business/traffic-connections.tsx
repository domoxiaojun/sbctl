import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { useQuery } from '@tanstack/react-query'
import { api } from '@/api/client'
import { DataState } from './common'
import { formatBytes, formatTime } from '@/lib/format'

type Props = { node?: string; protocol?: string; outbound?: string; from?: string; to?: string }

export function TrafficConnections({ node, protocol, outbound, from, to }: Props) {
 const [page, setPage] = useState(1)
 const [rankBy, setRankBy] = useState<'bytes' | 'connections'>('bytes')
 const pageSize = 20
 const tooLong = !!from && !!to && Date.parse(to) - Date.parse(from) >= 90 * 86_400_000
 const query = useQuery({ queryKey: ['traffic-connections', node, protocol, outbound, from, to, page, rankBy], queryFn: ({ signal }) => api.trafficConnections({ node, protocol, outbound, from, to, page, page_size: pageSize, rank_by: rankBy }, signal), refetchInterval: 15_000, enabled: !tooLong })
 if (tooLong) return <p className="text-sm text-muted-foreground">连接明细最多查询 90 天，请缩短日期范围。</p>
 if (query.isPending || query.error) return <DataState loading={query.isPending} error={query.error} retry={() => query.refetch()} />
 const report = query.data
 const summaries = [['domains', '域名'], ['destinations', '目标'], ['rules', '规则'], ['outbounds', '出站']] as const
 return <section className="panel overflow-hidden" aria-label="连接明细">
  <div className="p-5 border-b flex flex-wrap items-center justify-between gap-3">
   <div>
    <h2>连接明细与路由命中</h2>
    <p className="text-sm text-muted-foreground mt-1">保留 {report.retention_days} 天，默认按实际消耗字节统计热门目标，避免高频 DNS 小连接掩盖真实大流量。</p>
    {report.history_gap && <p className="mt-2 text-sm text-muted-foreground">部分连接历史缺失，排行基于已同步记录。</p>}
   </div>
   <div role="group" aria-label="热门聚合排序" className="flex gap-1">
    <Button type="button" size="sm" variant={rankBy === 'bytes' ? 'secondary' : 'ghost'} onClick={() => setRankBy('bytes')}>按流量排行</Button>
    <Button type="button" size="sm" variant={rankBy === 'connections' ? 'secondary' : 'ghost'} onClick={() => setRankBy('connections')}>按连接数排行</Button>
   </div>
  </div>
  <div className="grid gap-3 p-4 sm:grid-cols-2 lg:grid-cols-4">{summaries.map(([key, label]) => <div className="rounded-lg border p-3" key={key}><p className="text-xs text-muted-foreground">热门{label}</p><div className="mt-2 space-y-1.5">{report.summary[key].slice(0, 4).map(item => <div className="flex justify-between gap-2 text-sm" key={item.name}><span className="truncate" title={item.name}>{item.name}</span><span className="numeric text-xs text-muted-foreground shrink-0">{formatBytes((BigInt(item.upload_bytes) + BigInt(item.download_bytes)).toString())} · {item.connections}次</span></div>)}{!report.summary[key].length && <span className="text-sm text-muted-foreground">暂无记录</span>}</div></div>)}</div>
  <div className="overflow-x-auto"><table className="w-full text-sm"><thead><tr className="border-b text-left text-muted-foreground"><th className="p-3">时间</th><th className="p-3">节点 / 入口</th><th className="p-3">目标</th><th className="p-3">出站</th><th className="p-3">流量</th><th className="p-3">状态</th></tr></thead><tbody>
   {report.items.map(item => <tr className="border-b last:border-0" key={`${item.node_id}/${item.connection_id}`}><td className="p-3 whitespace-nowrap">{formatTime(item.started_at, true)}</td><td className="p-3"><span className="block text-xs text-muted-foreground">{item.node_id}</span><strong>{item.inbound || '—'}</strong><span className="block text-xs text-muted-foreground">{item.inbound_type || item.network || '—'}</span></td><td className="p-3 max-w-64 truncate" title={item.destination || item.domain}>{item.domain || item.destination || '—'}{item.source && <span className="block text-xs text-muted-foreground">源 {item.source}</span>}</td><td className="p-3">{item.outbound || '—'}<span className="block text-xs text-muted-foreground">{item.rule || '默认规则'}</span>{item.chain.length > 1 && <span className="block text-xs text-muted-foreground">{item.chain.join(' → ')}</span>}</td><td className="p-3 whitespace-nowrap tabular-nums">↑ {formatBytes(item.upload_bytes)} · ↓ {formatBytes(item.download_bytes)}</td><td className="p-3">{item.closed_at ? '已结束' : '进行中'}</td></tr>)}
   {!report.items.length && <tr><td className="p-6 text-center text-muted-foreground" colSpan={6}>当前区间没有连接明细。</td></tr>}
  </tbody></table></div>
  {report.total > pageSize && <div className="flex flex-wrap items-center justify-between gap-2 border-t p-4 text-sm">
   <span>第 {page} 页 · 共 {report.total} 条</span>
   <div className="flex gap-2">
    <Button variant="outline" disabled={page <= 1} onClick={() => setPage(page - 1)}>上一页</Button>
    <Button variant="outline" disabled={page * pageSize >= report.total} onClick={() => setPage(page + 1)}>下一页</Button>
   </div>
  </div>}
 </section>
}
