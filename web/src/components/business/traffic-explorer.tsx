import { lazy, Suspense, useState } from 'react'
import { Link, useNavigate, useSearch } from '@tanstack/react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowUpRight, RefreshCw, Search, SlidersHorizontal, X } from 'lucide-react'
import { api } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { DataState, FilterSelect } from './common'
import { ProtocolTraffic, LocalUserTraffic, ManagedUserTraffic } from './protocol-traffic'
import { TrafficDatePicker } from './traffic-date-picker'
import { TrafficRankings } from './traffic-rankings'
import { exactBytes, formatBytes, formatTime, totalBytes } from '@/lib/format'
import { dateError, knownTrafficNodes, presetDates, trafficTotals, type TrafficFilters } from '@/lib/traffic'
const TrafficTrend = lazy(() => import('./traffic-trend'))

export function TrafficExplorer({ nodeId, overview = false }: { nodeId?: string; overview?: boolean }) {
 const search = useSearch({ strict: false }), navigate = useNavigate(), client = useQueryClient()
 const [refreshMessage, setRefreshMessage] = useState(''), [advanced, setAdvanced] = useState(false)
 const scope = search.scope ?? 'inbounds'
 const defaults = presetDates(overview ? 'week' : 'all')
 const dates = search.from === undefined && search.to === undefined ? defaults : { from: search.from ?? '', to: search.to ?? '' }
 const update = (next: TrafficFilters) => { setRefreshMessage(''); void navigate({ to: '.', search: old => ({ ...old, ...dates, ...next, page: undefined }), resetScroll: false }) }
 const filters: TrafficFilters = { q: search.q, node: nodeId ?? search.node, ...dates, scope, ...(scope === 'inbounds' ? { protocol: search.protocol } : {}), ...(scope === 'managed' ? { user: search.user } : { activity: search.activity }), ...(scope === 'local' ? { local_user: search.local_user } : {}) }
 const error = dateError(filters.from, filters.to)
 const query = useQuery({ queryKey: ['traffic', filters], queryFn: ({ signal }) => api.traffic(filters, signal), enabled: !error, refetchInterval: 15_000 })
 const catalog = useQuery({ queryKey: ['traffic-catalog'], queryFn: ({ signal }) => api.traffic({}, signal), staleTime: 60_000 })
 const options = catalog.data?.nodes ?? query.data?.nodes ?? []
 const protocols = [...new Set(options.filter(n => !filters.node || n.id === filters.node).flatMap(n => n.inbounds.map(i => i.protocol)))].sort()
 const report = query.data, nodes = report ? knownTrafficNodes(report) : []
 const users = report?.users.filter(u => nodes.some(n => n.id === u.node_id)) ?? []
 const rows = scope === 'inbounds' ? nodes.flatMap(n => n.inbounds) : scope === 'local' ? nodes.flatMap(n => n.local_users) : users
 const sum = trafficTotals(rows), period = !!(filters.from && filters.to)
 const sampled = nodes.map(n => n.quality.sampled_at).filter((s): s is string => !!s).sort()[0]
 const hasFilters = !!(search.q || (!nodeId && search.node) || search.protocol || search.activity || search.user || search.local_user || period)
 const refresh = async () => {
  const before = JSON.stringify([query.data?.nodes, query.data?.users, query.data?.trend])
  const result = await query.refetch()
  if (nodeId) await client.invalidateQueries({ queryKey: ['node', nodeId] })
  if (overview) await client.invalidateQueries({ queryKey: ['overview'] })
  if (result.error) { setRefreshMessage('刷新失败，请重试。'); return }
  setRefreshMessage(before === JSON.stringify([result.data?.nodes, result.data?.users, result.data?.trend]) ? '已读取最新快照，尚无新的采样或用量变化。' : '统计已更新。')
 }
 const selectDay = (date: string) => update({ from: date, to: date })
 return <div className="space-y-5">
  <section className="panel p-4 sm:p-5 space-y-3" aria-label="流量筛选">
   <div className="flex flex-wrap items-center justify-between gap-3">
    <div role="group" aria-label="统计范围" className="flex flex-wrap gap-1">{([['inbounds', '协议入口'], ['managed', 'sbctl 用户'], ['local', '节点本地用户']] as const).map(([value, label]) => <Button key={value} className="min-h-11" variant={scope === value ? 'secondary' : 'ghost'} aria-pressed={scope === value} onClick={() => update({ scope: value, q: '', protocol: '', activity: '', user: '', local_user: '' })}>{label}</Button>)}</div>
    <Button className="min-h-11" variant="ghost" disabled={query.isFetching || !!error} onClick={() => { void refresh() }}><RefreshCw size={16} className={query.isFetching ? 'animate-spin motion-reduce:animate-none' : ''} />{query.isFetching ? '正在刷新' : '刷新统计'}</Button>
   </div>
   <div className="flex flex-wrap gap-2 items-center">
    <div className="relative grow basis-48"><Search className="absolute left-3 top-3.5 text-muted-foreground" size={16} /><Input className="pl-9 min-h-11" aria-label="搜索流量记录" placeholder={scope === 'inbounds' ? '搜索入口、协议或端口' : '搜索用户名称'} value={search.q ?? ''} onChange={e => update({ q: e.target.value })} /></div>
    {!nodeId && <div className="grow sm:grow-0 basis-36"><FilterSelect label="筛选流量节点" value={search.node || 'all'} onChange={v => update({ node: v === 'all' ? '' : v, protocol: '' })} options={[{ value: 'all', label: '全部节点' }, ...options.map(n => ({ value: n.id, label: n.name }))]} /></div>}
    {scope === 'inbounds' && <div className="grow sm:grow-0 basis-32"><FilterSelect label="筛选协议" value={search.protocol || 'all'} onChange={v => update({ protocol: v === 'all' ? '' : v })} options={[{ value: 'all', label: '全部协议' }, ...protocols.map(p => ({ value: p, label: p.toUpperCase() }))]} /></div>}
    <TrafficDatePicker from={dates.from} to={dates.to} onChange={update} />
    <Button className="min-h-11" variant={advanced ? 'secondary' : 'ghost'} aria-expanded={advanced} aria-controls="traffic-advanced" onClick={() => setAdvanced(!advanced)}><SlidersHorizontal size={16} />高级筛选{search.activity && ' · 1'}</Button>
    {hasFilters && <Button className="min-h-11" variant="ghost" onClick={() => update({ q: '', node: '', protocol: '', activity: '', user: '', local_user: '', from: '', to: '', sort: 'bytes' })}><X size={14} />重置筛选</Button>}
   </div>
   {advanced && <div id="traffic-advanced" className="border-t pt-3 grid gap-3 sm:grid-cols-2">
    {scope !== 'managed' && <div className="space-y-1.5 text-sm"><span>记录状态</span><FilterSelect label="筛选记录状态" value={search.activity || 'all'} onChange={v => update({ activity: v === 'all' ? '' : v })} options={[{ value: 'all', label: '当前与历史' }, { value: 'active', label: '当前记录' }, { value: 'removed', label: '已移除记录' }]} /></div>}
    <div className="space-y-1.5 text-sm"><span>明细排序</span><FilterSelect label="流量排序" value={search.sort === 'name' ? 'name' : 'bytes'} onChange={v => update({ sort: v as 'name' | 'bytes' })} options={[{ value: 'bytes', label: '用量从高到低' }, { value: 'name', label: '名称排序' }]} /></div>
   </div>}
   {(filters.user || filters.local_user) && <Button variant="outline" className="min-h-11" onClick={() => update({ user: '', local_user: '' })}>已定位用户：{filters.local_user || users.find(u => u.user_id === filters.user)?.display_name || '所选用户'}<X size={14} /><span className="sr-only">清除用户筛选</span></Button>}
   {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
  </section>
  <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground"><p role="status">{refreshMessage || (sampled ? `最近采样 ${formatTime(sampled, true)}` : '等待统计快照')}</p><span>每 15 秒更新 · 入口与用户用量分别统计</span></div>
  {error ? null : query.isPending || query.error ? <DataState loading={query.isPending} error={query.error} retry={() => query.refetch()} /> : report && <>
   <div className="grid gap-3 grid-cols-1 sm:grid-cols-3" aria-label="筛选结果汇总">{[['合计', totalBytes(sum)], ['上传', sum.upload_bytes], ['下载', sum.download_bytes]].map(([label, value]) => <div className="panel p-5" key={label}><p className="text-sm text-muted-foreground">{period ? '区间' : '累计'}{label}</p><strong className="text-2xl numeric block mt-2" title={nodes.length ? exactBytes(value) : undefined}>{nodes.length ? formatBytes(value) : '—'}</strong><p className="text-xs text-muted-foreground mt-2">当前筛选 · {rows.length} 条记录</p></div>)}</div>
   <Suspense fallback={<DataState loading />}><TrafficTrend report={report} onSelect={selectDay} /></Suspense>
   <TrafficRankings report={report} scope={scope} onSelect={update} />
   {period && <details className="panel p-4 text-xs text-muted-foreground" open={report.periods.some(p => p.status === 'unavailable')}><summary className="cursor-pointer min-h-7">{report.periods.some(p => p.status !== 'complete') ? '该时段记录不完整 · 查看记录范围' : '该时段记录完整 · 查看记录范围'}</summary><div className="space-y-2 mt-2">{report.periods.map(p => <p key={p.node_id}>{report.nodes.find(n => n.id === p.node_id)?.name ?? p.node_id}：{p.status === 'unavailable' ? '该时段无日期明细，不能按零流量判断。' : p.status === 'partial' ? '当前显示已记录部分。' : '该时段记录完整。'} 明细起点：{formatTime(p.history_started_at, true)}。</p>)}<p>跨日用量按结束采样归档，正常日期边界最多偏移 3 分钟。</p></div></details>}
   {overview ? <div className="flex justify-end"><Link to="/usage" search={{ ...search, ...dates }} className="inline-flex items-center gap-2 min-h-11 rounded-lg border px-4 text-sm font-medium hover:bg-muted focus-visible:outline-2 focus-visible:outline-ring">查看流量明细<ArrowUpRight size={16} /></Link></div> : <div className="space-y-3"><h2 className="text-sm text-muted-foreground">用量明细</h2>{scope === 'inbounds' ? <ProtocolTraffic key={JSON.stringify(filters)} nodes={nodes} period={period} sort={search.sort} /> : scope === 'local' ? <LocalUserTraffic key={JSON.stringify(filters)} nodes={nodes} period={period} sort={search.sort} /> : <ManagedUserTraffic key={JSON.stringify(filters)} users={users} period={period} sort={search.sort} />}</div>}
  </>}
 </div>
}
