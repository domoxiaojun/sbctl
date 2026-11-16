import { lazy, Suspense, type CSSProperties } from 'react'
import { Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { ArrowDownLeft, ArrowDownUp, ArrowRight, ArrowUpRight, Bell, CircleAlert, Globe2, ListChecks, RefreshCw, Server, Users } from 'lucide-react'
import { api } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { DataState, PageHeader } from '@/components/business/common'
import { CreateUserButton } from '@/components/business/user-actions'
import { OperationProgress } from '@/components/business/operation-progress'
import { byteParts, formatBytes, formatTime } from '@/lib/format'
const UsageChart = lazy(() => import('@/components/business/usage-chart'))

export default function Overview() {
  const query = useQuery({ queryKey: ['overview'], queryFn: ({ signal }) => api.overview(signal) })
  if (query.isPending || query.error) return <DataState loading={query.isPending} error={query.error} retry={() => query.refetch()} />
  const data = query.data
  const total = byteParts((BigInt(data.upload_bytes) + BigInt(data.download_bytes)).toString())
  const onlinePercent = data.node_count ? data.online_count / data.node_count * 100 : 0
  const degradedEnd = data.node_count ? (data.online_count + data.degraded_count) / data.node_count * 100 : 0

  return <>
    <PageHeader title="全局总览" description="节点、访问与用量，尽在一个工作空间。" action={<><Button variant="outline" aria-label="刷新总览" disabled={query.isFetching} onClick={() => query.refetch()}><RefreshCw className={query.isFetching ? 'animate-spin' : ''} /><span className="hide-small">刷新数据</span></Button><CreateUserButton /></>} />
    <div className="metric-grid">
      <Card className="metric-card gap-0 py-0"><CardContent className="p-5"><div className="metric-label">已登记节点<span className="metric-icon"><Server /></span></div><div className="metric-value">{data.node_count}<small>个节点</small></div><div className="metric-meta"><span className="metric-pill"><span className="online-dot" />{data.online_count} 在线</span><span>分布在 {data.regions.length} 个地区</span></div></CardContent></Card>
      <Card className="metric-card gap-0 py-0"><CardContent className="p-5"><div className="metric-label">已启用用户<span className="metric-icon violet"><Users /></span></div><div className="metric-value">{data.user_count}<small>位用户</small></div><div className="metric-meta"><Users size={13} /><span>独立身份，按组授权</span></div></CardContent></Card>
      <Card className="metric-card gap-0 py-0"><CardContent className="p-5"><div className="metric-label">受管用户累计<span className="metric-icon blue"><ArrowDownUp /></span></div><div className="metric-value">{total.value}<small>{total.unit}</small></div><div className="metric-meta"><span><ArrowUpRight />{formatBytes(data.upload_bytes)}</span><span><ArrowDownLeft />{formatBytes(data.download_bytes)}</span></div></CardContent></Card>
      <Card className="metric-card gap-0 py-0"><CardContent className="p-5"><div className="metric-label">待完成操作<span className="metric-icon amber"><ListChecks /></span></div><div className="metric-value">{data.pending_count}<small>项操作</small></div><div className="metric-meta"><Link to="/operations" className="metric-action">查看操作进度 <ArrowRight /></Link></div></CardContent></Card>
    </div>
    {data.pending_count > 0 && <Link to="/operations" search={{ status: 'partial' }} className="attention-banner"><span className="attention-icon"><CircleAlert size={18} /></span><div><strong>{data.pending_count} 项操作需要关注</strong><span>有节点仍待处理，查看各节点进度。</span></div><span className="attention-action">查看进度<ArrowRight size={16} /></span></Link>}
    <div className="overview-grid">
      <section className="panel usage-panel"><div className="panel-header"><div><h2>用户用量</h2><p>累计用量最高的 5 位用户</p></div><Link to="/usage" search={{ sort: 'bytes' }}>流量详情<ArrowUpRight size={15} /></Link></div><div className="chart-toolbar"><div className="chart-legend"><span><i className="legend-dot download" />下载</span><span><i className="legend-dot upload" />上传</span></div><span className="unit-label">单位随用量调整</span></div><Suspense fallback={<DataState loading />}><UsageChart items={data.top_usage} /></Suspense><div className="panel-footnote"><span>已记录累计，历史缺口单独标记</span><Link to="/usage">查看数据质量<ArrowRight size={14} /></Link></div></section>
      <section className="panel network-panel"><div className="panel-header"><div><h2>网络状态</h2><p>节点健康与地区分布</p></div><Link to="/nodes" search={{}}>全部节点<ArrowUpRight size={15} /></Link></div><div className="network-summary"><div className="health-donut" role="img" aria-label={`${data.online_count} 在线，${data.degraded_count} 需关注，${data.offline_count} 失联`} style={{ '--online-end': `${onlinePercent}%`, '--degraded-end': `${degradedEnd}%` } as CSSProperties}><div><strong>{data.online_count}<small>/{data.node_count}</small></strong><span>在线节点</span></div></div><div className="health-legend"><span><i className="good" />运行正常<strong>{data.online_count}</strong></span><span><i className="warn" />需要关注<strong>{data.degraded_count}</strong></span><span><i className="bad" />管理失联<strong>{data.offline_count}</strong></span></div></div><div className="region-list">{data.regions.map(region => <Link key={region.id} to="/nodes" search={{ region: region.id }}><span className="region-code">{region.id.toUpperCase()}</span><span className="region-name">{region.name}</span><div className="region-progress"><span style={{ width: `${region.node_count ? region.online_count / region.node_count * 100 : 0}%` }} /></div><span className="region-health">{region.online_count}<small> / {region.node_count}</small></span><Chevron /></Link>)}</div></section>
    </div>
    <div className="overview-bottom">
      <section className="panel"><div className="panel-header"><div><h2><ListChecks size={17} />最近操作</h2><p>跟踪每一次配置变更</p></div><Link to="/operations">全部记录<ArrowUpRight size={15} /></Link></div>{data.operations.map(operation => <OperationProgress operation={operation} key={operation.id} />)}</section>
      <section className="panel"><div className="panel-header"><div><h2><Bell size={17} />待关注事项<span className="count-badge">{data.alerts.length}</span></h2><p>需要你了解的运行情况</p></div><Link to="/alerts">查看全部<ArrowUpRight size={15} /></Link></div><div className="overview-alerts">{data.alerts.slice(0, 3).map(alert => <Link key={alert.id} to="/alerts"><span className={`alert-symbol ${alert.severity}`}><CircleAlert size={17} /></span><div><h3>{alert.title}</h3><p>{alert.description}</p><time>{formatTime(alert.created_at)}</time></div><ArrowUpRight size={15} /></Link>)}</div></section>
    </div>
    <p className="as-of"><span className="online-dot" />最近汇总 {formatTime(data.as_of, true)}<span>Asia/Singapore</span><Globe2 size={13} /></p>
  </>
}
function Chevron() { return <ArrowUpRight size={14} className="region-arrow" /> }
