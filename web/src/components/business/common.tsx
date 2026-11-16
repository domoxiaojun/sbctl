import { AlertCircle, ArrowLeft, ArrowUpDown, ChevronLeft, ChevronRight, Inbox, RefreshCw, Search, X } from 'lucide-react'
import { Link, useNavigate, useSearch } from '@tanstack/react-router'
import type { ReactNode } from 'react'
import type { Filters } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'

export function PageHeader({ title, description, action }: { title: string; description: string; action?: ReactNode; eyebrow?: string }) {
  return <div className="page-heading"><div><h1>{title}</h1><p>{description}</p></div>{action && <div className="page-actions">{action}</div>}</div>
}
export function DataState({ error, loading, retry, title }: { error?: Error | null; loading?: boolean; retry?: () => void; title?: string }) {
  if (loading) return <div aria-label="正在加载" role="status" className="space-y-4 p-6"><Skeleton className="h-6 w-40" /><Skeleton className="h-40 w-full" /><Skeleton className="h-12 w-full" /></div>
  const Icon = error ? AlertCircle : Inbox
  return <div className="empty-state" role={error ? 'alert' : 'status'}><div className="empty-icon"><Icon size={25} /></div><h3>{title ?? (error ? '暂时无法读取数据' : '没有匹配的记录')}</h3><p>{error?.message ?? '尝试调整筛选条件，或添加第一条记录。'}</p>{retry && <Button variant="outline" onClick={retry}><RefreshCw />重试</Button>}</div>
}
// eslint-disable-next-line react-refresh/only-export-components
export function useFilters() {
  const search = useSearch({ strict: false }), navigate = useNavigate()
  const filters: Filters = { archived: search.archived, q: search.q, status: search.status, region: search.region, sort: search.sort, page: search.page ?? 1, page_size: 8 }
  const update = (next: Partial<Filters>) => navigate({ to: '.', search: { archived: filters.archived, q: filters.q, status: filters.status, region: filters.region, sort: filters.sort, page: 1, ...next } })
  return { filters, update }
}
export function FilterSelect({ label, value, onChange, options, icon }: { label: string; value: string; onChange: (value: string) => void; options: { value: string; label: string }[]; icon?: ReactNode }) {
  return <div className="filter-select-field"><Select value={value} onValueChange={next => { if (next !== null) onChange(next) }} items={options}>
    <SelectTrigger aria-label={label} className="filter-select w-full">{icon}<SelectValue /></SelectTrigger>
    <SelectContent align="start" alignItemWithTrigger={false}>{options.map(option => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}</SelectContent>
  </Select></div>
}
export function FilterBar({ placeholder, filters, onChange, statuses = [], regions = false, sort = true }: { placeholder: string; filters: Filters; onChange: (filters: Partial<Filters>) => void; statuses?: [string, string][]; regions?: boolean; sort?: boolean }) {
  const hasFilters = !!(filters.q || filters.status || filters.region)
  return <div className="filter-bar"><div className="search-field"><Search size={17} /><Input className="pl-10 pr-9" aria-label={placeholder} value={filters.q ?? ''} placeholder={placeholder} onChange={event => onChange({ q: event.target.value })} />{filters.q && <button aria-label="清空搜索" onClick={() => onChange({ q: '' })}><X size={14} /></button>}</div><div className="filter-options">
    {statuses.length > 0 && <FilterSelect label="筛选状态" value={filters.status || 'all'} onChange={status => onChange({ status: status === 'all' ? '' : status })} options={[{ value: 'all', label: '全部状态' }, ...statuses.map(([value, label]) => ({ value, label }))]} />}
    {regions && <FilterSelect label="筛选地区" value={filters.region || 'all'} onChange={region => onChange({ region: region === 'all' ? '' : region })} options={[{ value: 'all', label: '全部地区' }, ...[['sg', '新加坡'], ['jp', '日本'], ['hk', '中国香港'], ['us', '美国'], ['de', '德国']].map(([value, label]) => ({ value, label }))]} />}
    {sort && <FilterSelect label="排序方式" icon={<ArrowUpDown size={14} />} value={filters.sort ?? 'name'} onChange={sort => onChange({ sort: sort as Filters['sort'] })} options={[{ value: 'name', label: '名称排序' }, { value: 'bytes', label: '累计用量' }]} />}
    {hasFilters && <Button variant="ghost" className="filter-reset" onClick={() => onChange({ q: '', status: '', region: '' })}><X />重置</Button>}
  </div></div>
}
export function Pagination({ data, onChange }: { data: { page: number; page_size: number; total: number }; onChange: (filters: Partial<Filters>) => void }) {
  const pages = Math.max(1, Math.ceil(data.total / data.page_size))
  return <div className="pagination"><span>共 <strong>{data.total}</strong> 条记录</span><div className="pagination-actions"><span>{data.page} / {pages} 页</span><Button aria-label="上一页" variant="outline" size="icon" disabled={data.page <= 1} onClick={() => onChange({ page: data.page - 1 })}><ChevronLeft /></Button><Button aria-label="下一页" variant="outline" size="icon" disabled={data.page >= pages} onClick={() => onChange({ page: data.page + 1 })}><ChevronRight /></Button></div></div>
}
export function BackLink({ to, label }: { to: '/nodes' | '/users' | '/operations'; label: string }) { return <Link to={to} search={{}} className="back-link"><ArrowLeft size={16} />{label}</Link> }
export function KeyValue({ label, children }: { label: string; children: ReactNode }) { return <div className="key-value"><dt>{label}</dt><dd>{children}</dd></div> }
