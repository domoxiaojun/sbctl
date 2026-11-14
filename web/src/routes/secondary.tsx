import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Bell, Check, Info, Monitor, Moon, Sun } from 'lucide-react'
import { api, isDemo } from '@/api/client'
import { Button } from '@/components/ui/button'
import { DataState, FilterBar, KeyValue, PageHeader, Pagination, useFilters } from '@/components/business/common'
import { StatusBadge } from '@/components/business/status'
import { formatTime } from '@/lib/format'
import { useTheme } from '@/lib/theme'
export function AlertsPage() {
  const { filters, update } = useFilters(), client = useQueryClient()
  const query = useQuery({ queryKey: ['alerts', filters], queryFn: ({ signal }) => api.alerts(filters, signal) })
  const ack = useMutation({ mutationFn: (id: string) => api.acknowledge(id, crypto.randomUUID()), onSuccess: async () => { await client.invalidateQueries({ queryKey: ['alerts'] }); await client.invalidateQueries({ queryKey: ['overview'] }) } })
  return <><PageHeader eyebrow="ATTENTION CENTER" title="告警中心" description="确认已看到的异常，继续追踪尚未恢复的问题。" /><section className="panel"><FilterBar placeholder="搜索告警…" filters={filters} onChange={update} sort={false} statuses={[[ 'open', '待处理' ], ['acknowledged', '已确认'], ['resolved', '已恢复']]} />{ack.error && <p className="field-error px-5" role="alert">{ack.error.message}</p>}{query.isPending || query.error ? <DataState loading={query.isPending} error={query.error} retry={() => query.refetch()} /> : <>{query.data.items.map(alert => <article key={alert.id} className="alert-item"><div className={`alert-icon ${alert.severity}`}><Bell size={18} /></div><div className="flex-1 min-w-0"><div className="flex items-center flex-wrap gap-2"><h2>{alert.title}</h2><StatusBadge status={alert.state} /></div><p>{alert.description}</p><time>{formatTime(alert.created_at)}</time></div>{alert.state === 'open' && <Button variant="outline" disabled={ack.isPending} onClick={() => ack.mutate(alert.id)}><Check size={15} />确认已看到</Button>}</article>)}{!query.data.items.length && <DataState title="没有待处理的告警" />}<Pagination data={query.data} onChange={update} /></>}</section><div className="notice mt-5"><Info size={16} /><p>确认告警不表示故障已恢复，也不会清除历史统计缺口。</p></div></>
}
export function SettingsPage() {
  const { theme, setTheme } = useTheme()
  return <><PageHeader eyebrow="PREFERENCES" title="设置与诊断" description="调整你的工作空间外观，了解当前运行环境。" /><div className="detail-grid"><section className="panel p-6"><h2>界面外观</h2><p className="text-sm text-muted-foreground mt-2 mb-6">选择主题，偏好只保存在当前浏览器。</p><div className="theme-options" role="group" aria-label="主题偏好">{[{ id: 'light', name: '浅色', icon: Sun }, { id: 'dark', name: '深色', icon: Moon }, { id: 'system', name: '跟随系统', icon: Monitor }].map(option => <button key={option.id} type="button" aria-pressed={theme === option.id} onClick={() => setTheme(option.id as 'light' | 'dark' | 'system')}><option.icon size={23} /><span>{option.name}</span>{theme === option.id && <Check size={13} />}</button>)}</div><dl className="mt-6"><KeyValue label="界面语言">简体中文</KeyValue><KeyValue label="显示时区">Asia/Singapore</KeyValue></dl></section><section className="panel p-6"><h2>当前环境</h2><dl className="mt-5"><KeyValue label="前端版本">0.1.0 / F0</KeyValue><KeyValue label="数据来源">{isDemo ? '本地演示数据' : '管理 API'}</KeyValue><KeyValue label="统计口径">业务字节累计</KeyValue><KeyValue label="生产节点接入">{isDemo ? '未接入' : '由管理 API 提供'}</KeyValue></dl><div className="notice mt-5"><p>服务器版本、证书、备份与只读诊断将在真实 API 接入后提供。当前页面不执行远程维护。</p></div></section></div></>
}
