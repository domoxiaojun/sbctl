import { lazy, Suspense } from 'react'
import { Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { api } from '@/api/client'
import type { Schema } from '@/api/types'
import { DataTable, type DisplayColumn } from '@/components/business/data-table'
import { DataState, FilterBar, PageHeader, Pagination, useFilters } from '@/components/business/common'
import { DataFreshness } from '@/components/business/status'
import { exactBytes, formatBytes, formatTime, totalBytes } from '@/lib/format'
const UsageChart = lazy(() => import('@/components/business/usage-chart'))
const columns: DisplayColumn<Schema['Usage']>[] = [{ id: 'name', label: '用户', render: u => <Link to="/users/$userId" params={{ userId: u.user_id }} className="font-medium hover:text-primary">{u.display_name}</Link> }, { id: 'upload', label: '上传', render: u => <span title={exactBytes(u.upload_bytes)}>{formatBytes(u.upload_bytes)}</span> }, { id: 'download', label: '下载', render: u => <span title={exactBytes(u.download_bytes)}>{formatBytes(u.download_bytes)}</span> }, { id: 'total', label: '合计', render: u => <strong title={exactBytes(totalBytes(u))} className="numeric">{formatBytes(totalBytes(u))}</strong> }, { id: 'start', label: '统计起点', render: u => <span className="text-muted-foreground text-xs">{formatTime(u.quality.started_at)}</span> }, { id: 'quality', label: '数据质量', render: u => <DataFreshness quality={u.quality} /> }]
export default function UsagePage() {
  const { filters, update } = useFilters(), query = useQuery({ queryKey: ['usage', filters], queryFn: ({ signal }) => api.usage(filters, signal) })
  return <><PageHeader eyebrow="USAGE & INTEGRITY" title="流量统计" description="已记录的业务字节累计，以及每份数据的完整性。" /><div className="notice mb-6"><p>累计不等于精确账单。存在历史缺口时保留已知值，未知损失不按零计算。</p></div><section className="panel"><FilterBar placeholder="搜索用户用量…" filters={filters} onChange={update} />{query.isPending || query.error ? <DataState loading={query.isPending} error={query.error} retry={() => query.refetch()} /> : <><div className="px-5 pt-5"><h2>当前结果对比</h2><p className="text-xs text-muted-foreground mt-1">展示本页用户的累计合计</p><Suspense fallback={<DataState loading />}><UsageChart items={query.data.items} /></Suspense></div><div className="desktop-table"><DataTable data={query.data.items} columns={columns} rowId={u => u.user_id} /></div><div className="mobile-cards">{query.data.items.map(u => <article key={u.user_id} className="mobile-record"><div className="flex justify-between gap-3"><Link to="/users/$userId" params={{ userId: u.user_id }} className="font-medium">{u.display_name}</Link><strong>{formatBytes(totalBytes(u))}</strong></div><div className="record-metrics"><span>上传 {formatBytes(u.upload_bytes)}</span><span>下载 {formatBytes(u.download_bytes)}</span></div><DataFreshness quality={u.quality} /></article>)}</div>{!query.data.items.length && <DataState />}<Pagination data={query.data} onChange={update} /></>}</section></>
}
