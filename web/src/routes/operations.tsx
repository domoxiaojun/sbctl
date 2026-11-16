import { useQuery } from '@tanstack/react-query'
import { Link, useParams } from '@tanstack/react-router'
import { api } from '@/api/client'
import { BackLink, DataState, FilterBar, PageHeader, Pagination, useFilters } from '@/components/business/common'
import { OperationProgress } from '@/components/business/operation-progress'
import { operationNames } from '@/lib/format'
export function OperationsPage() {
  const { filters, update } = useFilters(), query = useQuery({ queryKey: ['operations', filters], queryFn: ({ signal }) => api.operations(filters, signal), refetchInterval: q => q.state.data?.items.some(op => ['queued', 'running', 'partial'].includes(op.state)) ? 1000 : false })
  return <><PageHeader eyebrow="OPERATIONS" title="操作记录" description="从意图提交到节点验证，每个阶段都有明确结果。" /><section className="panel"><FilterBar placeholder="搜索操作对象…" filters={filters} onChange={update} sort={false} statuses={[[ 'queued', '已接收' ], ['running', '执行中'], ['partial', '部分完成'], ['completed', '已完成']]} />{query.isPending || query.error ? <DataState loading={query.isPending} error={query.error} retry={() => query.refetch()} /> : <>{query.data.items.map(op => <OperationProgress key={op.id} operation={op} />)}{!query.data.items.length && <DataState />}<Pagination data={query.data} onChange={update} /></>}</section></>
}
export function OperationDetail() {
  const { operationId = '' } = useParams({ strict: false }), query = useQuery({ queryKey: ['operation', operationId], queryFn: ({ signal }) => api.operation(operationId, signal), refetchInterval: q => q.state.data && ['queued', 'running', 'partial'].includes(q.state.data.state) ? 1000 : false })
  if (query.isPending || query.error) return <DataState loading={query.isPending} error={query.error} retry={() => query.refetch()} />
  const op = query.data
  return <><BackLink to="/operations" label="返回操作记录" /><PageHeader title={`${operationNames[op.kind]} · ${op.subject}`} description="分别查看节点应用、实际验证与订阅状态。" />{op.state === 'partial' && <div className="notice notice-warning mb-5"><p>仍有节点待处理。失联节点恢复后优先执行，不能将部分完成视为全部生效。</p></div>}<section className="panel p-6"><div className="operation-id">操作编号 <code>{op.id}</code></div><OperationProgress operation={op} detailed /></section>{op.user_id && <Link className="back-link mt-6" to="/users/$userId" params={{ userId: op.user_id }}>查看用户详情 →</Link>}</>
}
