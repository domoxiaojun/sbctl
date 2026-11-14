import { AlertCircle, ArrowLeft, ChevronLeft, ChevronRight, Inbox, RefreshCw, Search } from 'lucide-react'
import { Link, useNavigate, useSearch } from '@tanstack/react-router'
import type { ReactNode } from 'react'
import type { Filters } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'

export function PageHeader({ title, description, action, eyebrow }: { title: string; description: string; action?: ReactNode; eyebrow?: string }) {
  return <div className="page-heading"><div>{eyebrow && <div className="eyebrow mb-2">{eyebrow}</div>}<h1>{title}</h1><p className="mt-2 text-sm text-muted-foreground leading-relaxed">{description}</p></div>{action && <div className="flex shrink-0 items-center gap-2">{action}</div>}</div>
}
export function DataState({ error, loading, retry, title }: { error?: Error | null; loading?: boolean; retry?: () => void; title?: string }) {
  if (loading) return <div aria-label="正在加载" role="status" className="space-y-4 p-6"><Skeleton className="h-6 w-40" /><Skeleton className="h-40 w-full" /><Skeleton className="h-12 w-full" /></div>
  const Icon = error ? AlertCircle : Inbox
  return <div className="empty-state" role={error ? 'alert' : 'status'}><div className="empty-icon"><Icon size={24} /></div><h3>{title ?? (error ? '暂时无法读取数据' : '没有匹配的记录')}</h3><p>{error?.message ?? '尝试调整筛选条件，或添加第一条记录。'}</p>{retry && <Button variant="outline" onClick={retry}><RefreshCw size={15} />重试</Button>}</div>
}
// eslint-disable-next-line react-refresh/only-export-components
export function useFilters() {
  const search = useSearch({ strict: false })
  const navigate = useNavigate()
  const filters: Filters = { ...search, page: search.page ?? 1, page_size: 8 }
  const update = (next: Partial<Filters>) => navigate({ to: '.', search: old => ({ ...old, page: 1, ...next }) })
  return { filters, update }
}
export function FilterBar({ placeholder, filters, onChange, statuses = [], regions = false, sort = true }: { placeholder: string; filters: Filters; onChange: (f: Partial<Filters>) => void; statuses?: [string, string][]; regions?: boolean; sort?: boolean }) {
  return <div className="filter-bar"><div className="relative min-w-0 flex-1"><Search className="search-icon" size={16} /><Input aria-label={placeholder} value={filters.q ?? ''} placeholder={placeholder} onChange={e => onChange({ q: e.target.value })} className="pl-9" /></div><div className="flex flex-wrap gap-2">
    {statuses.length > 0 && <select aria-label="筛选状态" className="select-control" value={filters.status ?? ''} onChange={e => onChange({ status: e.target.value })}><option value="">全部状态</option>{statuses.map(([value, label]) => <option value={value} key={value}>{label}</option>)}</select>}
    {regions && <select aria-label="筛选地区" className="select-control" value={filters.region ?? ''} onChange={e => onChange({ region: e.target.value })}><option value="">全部地区</option>{[['sg', '新加坡'], ['jp', '日本'], ['hk', '中国香港'], ['us', '美国'], ['de', '德国']].map(([value, label]) => <option value={value} key={value}>{label}</option>)}</select>}
    {sort && <select aria-label="排序方式" className="select-control" value={filters.sort ?? 'name'} onChange={e => onChange({ sort: e.target.value as Filters['sort'] })}><option value="name">名称排序</option><option value="bytes">累计用量 ↓</option></select>}
  </div></div>
}
export function Pagination({ data, onChange }: { data: { page: number; page_size: number; total: number }; onChange: (f: Partial<Filters>) => void }) {
  const pages = Math.max(1, Math.ceil(data.total / data.page_size))
  return <div className="pagination"><span>共 <strong>{data.total}</strong> 条记录</span><div className="flex items-center gap-3"><span>{data.page} / {pages} 页</span><Button aria-label="上一页" variant="outline" size="icon" disabled={data.page <= 1} onClick={() => onChange({ page: data.page - 1 })}><ChevronLeft size={16} /></Button><Button aria-label="下一页" variant="outline" size="icon" disabled={data.page >= pages} onClick={() => onChange({ page: data.page + 1 })}><ChevronRight size={16} /></Button></div></div>
}
export function BackLink({ to, label }: { to: '/nodes' | '/users' | '/operations'; label: string }) { return <Link to={to} search={{}} className="back-link"><ArrowLeft size={15} />{label}</Link> }
export function KeyValue({ label, children }: { label: string; children: ReactNode }) { return <div className="key-value"><dt>{label}</dt><dd>{children}</dd></div> }
