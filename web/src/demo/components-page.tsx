import { useState, lazy, Suspense } from 'react'
import { PageHeader, DataState } from '@/components/business/common'
import { DataFreshness, StatusBadge } from '@/components/business/status'
import { CreateUserButton } from '@/components/business/user-actions'
import { fixtures } from './fixtures'
import { DataTable, type DisplayColumn } from '@/components/business/data-table'
import type { User } from '@/api/types'
import { formatBytes, totalBytes } from '@/lib/format'
const UsageChart = lazy(() => import('@/components/business/usage-chart'))
const sample = fixtures(Date.now())
const sampleColumns: DisplayColumn<User>[] = [
  { id: 'name', label: '用户', render: user => user.display_name },
  { id: 'status', label: '状态', render: user => <StatusBadge status={user.status} /> },
  { id: 'usage', label: '累计', render: user => formatBytes(totalBytes(user)) },
]
export default function ComponentsPage() {
  const [state, setState] = useState('ready')
  return <><PageHeader eyebrow="COMPONENT LAB" title="组件与状态验收" description="仅开发环境可用：检查表单、弹窗、表格、图表及反馈状态。" action={<CreateUserButton />} /><section className="panel p-6"><h2>统一状态</h2><div className="flex gap-3 flex-wrap my-5">{['online', 'offline', 'degraded', 'queued', 'running', 'partial', 'completed', 'revoked'].map(status => <StatusBadge key={status} status={status} />)}</div><label className="text-sm font-medium" htmlFor="lab-state">数据反馈场景</label><select id="lab-state" className="select-control ml-3" value={state} onChange={e => setState(e.target.value)}><option value="ready">正常数据</option><option value="loading">加载中</option><option value="empty">空数据</option><option value="error">读取失败</option><option value="stale">过期且有历史缺口</option></select><div className="mt-6">{state === 'ready' ? <Suspense fallback={<DataState loading />}><UsageChart items={sample.users.slice(0, 5).map(u => ({ ...u, user_id: u.id }))} /></Suspense> : state === 'stale' ? <div className="notice"><DataFreshness quality={{ ...sample.users[0].quality, freshness: 'stale', has_historical_gap: true, gap_count: 2 }} /><p>最后已记录累计仍保留，未知损失不显示为零。</p></div> : <DataState loading={state === 'loading'} error={state === 'error' ? new Error('演示请求失败。点击重试恢复正常数据。') : undefined} retry={state === 'error' ? () => setState('ready') : undefined} />}</div></section><section className="panel mt-6"><div className="panel-header mb-5"><h2>数据表格</h2><span className="text-xs text-muted-foreground">同一组件与生成类型</span></div><DataTable data={sample.users.slice(0, 3)} columns={sampleColumns} rowId={user => user.id} /></section><p className="page-note">节点和用户页面提供同一套表格与移动卡片，操作页提供阶段进度。此页与演示夹具均不包含在生产发行物中。</p></>
}
