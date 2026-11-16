import { useState } from 'react'
import { useNavigate, useSearch } from '@tanstack/react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { RefreshCw, Search } from 'lucide-react'
import { api } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { DataState, FilterSelect } from './common'
import { ProtocolTraffic, LocalUserTraffic, ManagedUserTraffic } from './protocol-traffic'
import { formatBytes, formatTime, totalBytes } from '@/lib/format'
import { calendarDate, dateError, knownTrafficNodes, presetDates, trafficTotals, type TrafficFilters } from '@/lib/traffic'

export function TrafficExplorer({ nodeId }: { nodeId?: string }) {
 const search = useSearch({ strict: false }), navigate = useNavigate()
 const client = useQueryClient()
 const [refreshMessage, setRefreshMessage] = useState('')
 const scope = search.scope ?? 'inbounds'
 const update = (next: TrafficFilters) => { setRefreshMessage(''); void navigate({ to: '.', search: old => ({ ...old, ...next, page: undefined }) }) }
 const filters: TrafficFilters = { q: search.q, node: nodeId ?? search.node, from: search.from, to: search.to, ...(scope === 'inbounds' ? { protocol: search.protocol } : {}), ...(scope !== 'managed' ? {activity: search.activity} : {}) }
 const error = dateError(filters.from, filters.to)
 const query = useQuery({ queryKey: ['traffic', filters], queryFn: ({ signal }) => api.traffic(filters, signal), enabled: !error, refetchInterval: 15_000 })
 const catalog = useQuery({ queryKey: ['traffic-catalog'], queryFn: ({ signal }) => api.traffic({}, signal), staleTime: 60_000 })
 const options = catalog.data?.nodes ?? query.data?.nodes ?? []
 const protocols = [...new Set(options.filter(n => !filters.node || n.id === filters.node).flatMap(n => n.inbounds.map(i => i.protocol)))].sort()
 const report = query.data
 const nodes = report ? knownTrafficNodes(report) : []
 const users = report?.users.filter(u => nodes.some(n => n.id === u.node_id)) ?? []
 const rows = scope === 'inbounds' ? nodes.flatMap(n => n.inbounds) : scope === 'local' ? nodes.flatMap(n => n.local_users) : users
 const sum = trafficTotals(rows)
 const period = !!(filters.from && filters.to)
 const sampled = nodes.map(n => n.quality.sampled_at).filter((s): s is string => !!s).sort()[0]
 const refresh = async () => {
  const before = JSON.stringify(query.data)
  const result = await query.refetch()
  if (nodeId) await client.invalidateQueries({queryKey:['node',nodeId]})
  if (result.error) { setRefreshMessage('刷新失败，请重试。'); return }
  // as_of changes on every request; freshness and byte counters are the useful signals.
  const previous = JSON.parse(before || 'null') as typeof report
  setRefreshMessage(previous && JSON.stringify(previous.nodes) === JSON.stringify(result.data?.nodes) ? '已读取最新快照，尚无新的采样或用量变化。' : '统计已更新。')
 }
 return <div className="space-y-5">
  <section className="panel p-4 sm:p-5 space-y-4" aria-label="流量筛选">
   <div className="flex flex-wrap items-center justify-between gap-3">
    <div role="group" aria-label="统计范围" className="flex flex-wrap gap-2">{([['inbounds','协议入口'],['managed','sbctl 用户'],['local','节点本地用户']] as const).map(([value,label]) => <Button key={value} className="min-h-11" variant={scope === value ? 'default' : 'outline'} aria-pressed={scope === value} onClick={() => update({ scope: value, q: '', protocol: '', activity: '' })}>{label}</Button>)}</div>
    <Button className="min-h-11" variant="outline" disabled={query.isFetching || !!error} onClick={() => {void refresh()}}><RefreshCw size={16} className={query.isFetching ? 'animate-spin motion-reduce:animate-none' : ''}/>{query.isFetching ? '正在刷新' : '刷新统计'}</Button>
   </div>
   <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
    <label className="space-y-1.5 text-sm"><span>搜索{scope === 'inbounds' ? '入口 / 端口' : '用户'}</span><div className="relative"><Search className="absolute left-3 top-3.5 text-muted-foreground" size={16}/><Input className="pl-9 min-h-11" aria-label="搜索流量记录" placeholder={scope === 'inbounds' ? '例如 vmess、23323' : '用户名称或编号'} value={search.q ?? ''} onChange={e => update({q:e.target.value})}/></div></label>
    {!nodeId && <div className="space-y-1.5 text-sm"><span>节点</span><FilterSelect label="筛选流量节点" value={search.node || 'all'} onChange={v => update({ node: v === 'all' ? '' : v, protocol: '' })} options={[{value:'all',label:'全部节点'},...options.map(n => ({value:n.id,label:n.name}))]}/></div>}
    {scope === 'inbounds' && <div className="space-y-1.5 text-sm"><span>协议</span><FilterSelect label="筛选协议" value={search.protocol || 'all'} onChange={v => update({protocol:v === 'all' ? '' : v})} options={[{value:'all',label:'全部协议'},...protocols.map(p => ({value:p,label:p.toUpperCase()}))]}/></div>}
    {scope !== 'managed' && <div className="space-y-1.5 text-sm"><span>记录状态</span><FilterSelect label="筛选记录状态" value={search.activity || 'all'} onChange={v => update({activity:v === 'all' ? '' : v})} options={[{value:'all',label:'当前与历史'},{value:'active',label:'当前记录'},{value:'removed',label:'已移除记录'}]}/></div>}
   </div>
   <div className="border-t pt-4 space-y-3">
    <div className="flex flex-wrap gap-2" role="group" aria-label="日期快捷选择">{[['all','全部累计'],['today','今天'],['yesterday','昨天'],['week','近 7 天'],['month','本月']].map(([value,label]) => { const dates = presetDates(value); const selected = (search.from || '') === dates.from && (search.to || '') === dates.to; return <Button className="min-h-11" key={value} aria-pressed={selected} variant={selected ? 'secondary' : 'ghost'} onClick={() => update(dates)}>{label}</Button> })}</div>
    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4 items-end">
     <label className="space-y-1.5 text-sm">开始日期<Input type="date" aria-label="开始日期" className="min-h-11" value={search.from || ''} max={search.to || calendarDate()} aria-invalid={!!error} onChange={e => update({from:e.target.value})}/></label>
     <label className="space-y-1.5 text-sm">结束日期<Input type="date" aria-label="结束日期" className="min-h-11" value={search.to || ''} min={search.from} max={calendarDate()} aria-invalid={!!error} onChange={e => update({to:e.target.value})}/></label>
     <div className="space-y-1.5 text-sm"><span>排序</span><FilterSelect label="流量排序" value={search.sort === 'name' ? 'name' : 'bytes'} onChange={v => update({sort:v as 'name'|'bytes'})} options={[{value:'bytes',label:'用量从高到低'},{value:'name',label:'名称排序'}]}/></div>
     <Button className="min-h-11" variant="ghost" onClick={() => update({q:'',node:'',protocol:'',activity:'',from:'',to:'',sort:'bytes'})}>重置筛选</Button>
    </div>
    <p className="text-xs text-muted-foreground">日期按 UTC+8，包含结束日期全天。每 15 秒更新；刷新读取最近采样，数据可能有数十秒延迟。</p>
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
   </div>
  </section>
  <p role="status" className="text-xs text-muted-foreground min-h-4">{refreshMessage || (sampled ? `最近采样 ${formatTime(sampled, true)}` : '等待统计快照')}</p>
  {error ? null : query.isPending || query.error ? <DataState loading={query.isPending} error={query.error} retry={() => query.refetch()}/> : report && <>
   {period && <div className="notice space-y-1"><p>{filters.from} 至 {filters.to} · 区间已记录用量。跨日流量按采样结束时间归档，正常边界误差不超过 3 分钟。</p>{report.periods.map(p => <p key={p.node_id}>{report.nodes.find(n => n.id === p.node_id)?.name ?? p.node_id}：{p.status === 'unavailable' ? '该时段无日期明细，不能按零流量判断。' : p.status === 'partial' ? '该时段记录不完整，当前显示已记录部分。' : '该时段记录完整。'} 明细起点：{formatTime(p.history_started_at, true)}。</p>)}</div>}
   <div className="grid gap-3 sm:grid-cols-3" aria-label="筛选结果汇总">{[['合计',totalBytes(sum)],['上传',sum.upload_bytes],['下载',sum.download_bytes]].map(([label,value]) => <div className="panel p-5" key={label}><p className="text-sm text-muted-foreground">{period ? '区间' : '累计'}{label}</p><strong className="text-2xl numeric block mt-2">{nodes.length ? formatBytes(value) : '—'}</strong><p className="text-xs text-muted-foreground mt-2">当前筛选 · {rows.length} 条记录</p></div>)}</div>
   {scope === 'inbounds' ? <ProtocolTraffic nodes={nodes} period={period} sort={search.sort}/> : scope === 'local' ? <LocalUserTraffic nodes={nodes} period={period} sort={search.sort}/> : <ManagedUserTraffic users={users} period={period} sort={search.sort}/>}
  </>}
 </div>
}
