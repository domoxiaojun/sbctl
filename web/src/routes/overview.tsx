import type { CSSProperties } from 'react'
import { Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Activity, ArrowDownUp, ArrowRight, ArrowUpRight, Bell, CircleAlert, Globe2, ListChecks, RefreshCw, Server, Users } from 'lucide-react'
import { api } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { DataState, PageHeader } from '@/components/business/common'
import { OperationProgress } from '@/components/business/operation-progress'
import { formatRate, formatTime } from '@/lib/format'

export default function Overview() {
  const query = useQuery({ queryKey: ['overview'], queryFn: ({ signal }) => api.overview(signal), refetchInterval: 5_000 })
  if (query.isPending || query.error) return <DataState loading={query.isPending} error={query.error} retry={() => query.refetch()} />
  const data = query.data
  const onlinePercent = data.node_count ? data.online_count / data.node_count * 100 : 0
  const degradedEnd = data.node_count ? (data.online_count + data.degraded_count) / data.node_count * 100 : 0

  return <>
    <PageHeader title="全局总览" description="查看单容器节点运行状况、实时吞吐与待办事项。" action={<><Button variant="outline" aria-label="刷新总览" disabled={query.isFetching} onClick={() => { void query.refetch() }}><RefreshCw className={query.isFetching ? 'animate-spin' : ''} /><span className="hide-small">刷新数据</span></Button></>} />
    <div className="metric-grid">
      <Card className="metric-card gap-0 py-0"><CardContent className="p-5"><div className="metric-label">已登记节点<span className="metric-icon"><Server /></span></div><div className="metric-value">{data.node_count}<small>个节点</small></div><div className="metric-meta"><span className="metric-pill"><span className="online-dot" />{data.online_count} 在线</span><span>分布在 {data.regions.length} 个地区</span></div></CardContent></Card>
      <Card className="metric-card gap-0 py-0"><CardContent className="p-5"><div className="metric-label">实时总吞吐<span className="metric-icon"><Activity /></span></div><div className="metric-value tabular-nums text-xl sm:text-2xl">↓ {formatRate(data.download_bps)}</div><div className="metric-meta"><span className="tabular-nums">↑ {formatRate(data.upload_bps)}</span><span>· {data.active_connections} 活跃连接</span></div></CardContent></Card>
      <Card className="metric-card gap-0 py-0"><CardContent className="p-5"><div className="metric-label">已启用用户<span className="metric-icon violet"><Users /></span></div><div className="metric-value">{data.user_count}<small>位用户</small></div><div className="metric-meta"><Users size={13} /><span>独立身份，按组授权</span></div></CardContent></Card>
      <Card className="metric-card gap-0 py-0"><CardContent className="p-5"><div className="metric-label">需关注与待办<span className="metric-icon amber"><ListChecks /></span></div><div className="metric-value">{data.degraded_count + data.offline_count + data.pending_count}<small>项</small></div><div className="metric-meta"><span>{data.degraded_count + data.offline_count} 节点关注 · </span><Link to="/operations" className="metric-action">{data.pending_count} 待办 <ArrowRight /></Link></div></CardContent></Card>
    </div>
    {data.pending_count > 0 && <Link to="/operations" search={{ status: 'partial' }} className="attention-banner"><span className="attention-icon"><CircleAlert size={18} /></span><div><strong>{data.pending_count} 项操作需要关注</strong><span>有节点仍待处理，查看各节点进度。</span></div><span className="attention-action">查看进度<ArrowRight size={16} /></span></Link>}
    <section className="grid gap-3 mb-6 md:grid-cols-3" aria-label="板块入口">
      {[
        { to: '/nodes', title: '节点与协议', description: '单容器 sing-box 热重载、协议入口与月流量账单', icon: Server },
        { to: '/users', title: '用户与授权', description: '用户身份、节点组授权与订阅交付', icon: Users },
        { to: '/usage', title: '流量与实时监控', description: '实时速率流、活跃连接、入口与出站多维趋势排行', icon: ArrowDownUp },
      ].map(item => <Link key={item.to} to={item.to} search={{}} className="panel p-5 flex items-start gap-3 hover:bg-muted/50 focus-visible:outline-2 focus-visible:outline-ring"><item.icon className="text-primary shrink-0 mt-0.5" size={20} /><div className="flex-1"><h2 className="text-sm">{item.title}</h2><p className="text-xs text-muted-foreground mt-2 leading-5">{item.description}</p></div><ArrowUpRight size={16} className="text-muted-foreground shrink-0" /></Link>)}
    </section>
    <div className="overview-grid">
      <section className="panel network-panel"><div className="panel-header"><div><h2>网络状态</h2><p>节点健康与地区分布</p></div><Link to="/nodes" search={{}}>全部节点<ArrowUpRight size={15} /></Link></div><div className="network-summary"><div className="health-donut" role="img" aria-label={`${data.online_count} 在线，${data.degraded_count} 需关注，${data.offline_count} 失联`} style={{ '--online-end': `${onlinePercent}%`, '--degraded-end': `${degradedEnd}%` } as CSSProperties}><div><strong>{data.online_count}<small>/{data.node_count}</small></strong><span>在线节点</span></div></div><div className="health-legend"><span><i className="good" />运行正常<strong>{data.online_count}</strong></span><span><i className="warn" />需要关注<strong>{data.degraded_count}</strong></span><span><i className="bad" />管理失联<strong>{data.offline_count}</strong></span></div></div><div className="region-list">{data.regions.map(region => <Link key={region.id} to="/nodes" search={{ region: region.id }}><span className="region-code">{region.id.toUpperCase()}</span><span className="region-name">{region.name}</span><div className="region-progress"><span style={{ width: `${region.node_count ? region.online_count / region.node_count * 100 : 0}%` }} /></div><span className="region-health">{region.online_count}<small> / {region.node_count}</small></span><Chevron /></Link>)}</div></section>
      <section className="panel"><div className="panel-header"><div><h2><ListChecks size={17} />最近操作</h2><p>跟踪用户与授权变更</p></div><Link to="/operations">全部记录<ArrowUpRight size={15} /></Link></div>{data.operations.length ? data.operations.map(operation => <OperationProgress operation={operation} key={operation.id} />) : <p className="p-5 text-sm text-muted-foreground">暂无操作记录。</p>}</section>
    </div>
    <div className="mb-6">
      <section className="panel"><div className="panel-header"><div><h2><Bell size={17} />待关注事项<span className="count-badge">{data.alerts.length}</span></h2><p>需要你了解的运行与月配额情况</p></div><Link to="/alerts">查看全部<ArrowUpRight size={15} /></Link></div><div className="overview-alerts">{!data.alerts.length && <p className="p-5 text-sm text-muted-foreground">暂无待关注事项。</p>}{data.alerts.slice(0, 3).map(alert => <Link key={alert.id} to="/alerts"><span className={`alert-symbol ${alert.severity}`}><CircleAlert size={17} /></span><div><h3>{alert.title}</h3><p>{alert.description}</p><time>{formatTime(alert.created_at)}</time></div><ArrowUpRight size={15} /></Link>)}</div></section>
    </div>
    <p className="as-of"><span className="online-dot" />最近汇总 {formatTime(data.as_of, true)}<span>Asia/Singapore</span><Globe2 size={13} /></p>
  </>
}
function Chevron() { return <ArrowUpRight size={14} className="region-arrow" /> }
