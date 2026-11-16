import { useState } from 'react'
import { useQuery, useMutation } from '@tanstack/react-query'
import { Link, useParams } from '@tanstack/react-router'
import { ArrowUpRight, Check, Copy, Link2 } from 'lucide-react'
import { api, isDemo } from '@/api/client'
import type { User } from '@/api/types'
import { DataTable, type DisplayColumn } from '@/components/business/data-table'
import { BackLink, DataState, FilterBar, KeyValue, PageHeader, Pagination, useFilters } from '@/components/business/common'
import { CreateUserButton, DisableUserButton, EnableUserButton } from '@/components/business/user-actions'
import { UserGrantsButton } from '@/components/business/user-grants'
import { StatusBadge } from '@/components/business/status'
import { Button } from '@/components/ui/button'
import { formatTime } from '@/lib/format'
const userLink = (u: User) => <Link to="/users/$userId" params={{ userId: u.id }} search={{}} className="entity-link"><div className="avatar user-avatar">{u.display_name.slice(0, 1)}</div><div><strong>{u.display_name}</strong><small>{u.note || '未添加备注'}</small></div></Link>
const columns: DisplayColumn<User>[] = [{ id: 'name', label: '用户', render: userLink }, { id: 'status', label: '授权状态', render: u => <StatusBadge status={u.status} /> }, { id: 'nodes', label: '授权节点', render: u => <span>{u.node_count} 个</span> }, { id: 'subscription', label: '订阅', render: u => <StatusBadge status={u.subscription_state} /> }, { id: 'detail', label: '', render: u => <Link to="/users/$userId" params={{ userId: u.id }} aria-label={`管理${u.display_name}`} className="icon-link"><ArrowUpRight size={16} /></Link> }]
export function UsersPage() {
  const { filters, update } = useFilters(), query = useQuery({ queryKey: ['users', filters], queryFn: ({ signal }) => api.users(filters, signal) })
  return <><PageHeader eyebrow="ACCESS MANAGEMENT" title="用户与授权" description="为每位用户分配独立访问权限，保留完整的变更记录。" action={<CreateUserButton />} /><section className="panel"><FilterBar placeholder="搜索用户名称…" filters={filters} onChange={update} statuses={[[ 'active', '已启用' ], ['provisioning', '配置中'], ['disabled', '已禁用']]} sort={false} />{query.isPending || query.error ? <DataState loading={query.isPending} error={query.error} retry={() => query.refetch()} /> : <><div className="desktop-table"><DataTable data={query.data.items} columns={columns} rowId={u => u.id} /></div><div className="mobile-cards">{query.data.items.map(u => <article className="mobile-record" key={u.id}><div className="flex justify-between gap-2 items-center">{userLink(u)}<StatusBadge status={u.status} /></div><div className="record-metrics"><span>节点 <strong>{u.node_count} 个</strong></span></div><StatusBadge status={u.subscription_state} /></article>)}{!query.data.items.length && <DataState />}</div><Pagination data={query.data} onChange={update} /></>}</section></>
}
function SubscriptionPanel({ user }: { user: User }) {
  const [links, setLinks] = useState<{ label: string; url: string }[]>([])
  const [copied, setCopied] = useState(''), [copyError, setCopyError] = useState('')
  const subscription = useQuery({ queryKey: ['subscription', user.id, user.revision], queryFn: ({ signal }) => api.subscription(user.id, signal), refetchInterval: q => q.state.data && ['pending', 'revoke_pending'].includes(q.state.data.state) ? 2000 : false })
  const reveal = useMutation({ gcTime: 0, mutationFn: () => api.subscriptionLink(user.id, crypto.randomUUID()), onSuccess: result => {
    setLinks([{ label: 'Surge', url: result.url }, ...(result.mihomo_url ? [{ label: 'Mihomo', url: result.mihomo_url }] : [])]); reveal.reset()
  } })
  return <section className="panel p-6"><div className="flex justify-between items-center"><h2>订阅交付</h2><Link2 size={18} className="text-muted-foreground" /></div>{subscription.isPending || subscription.error ? <DataState loading={subscription.isPending} error={subscription.error} retry={() => subscription.refetch()} /> : <>
    <div className="flex items-center gap-3 mt-6"><StatusBadge status={subscription.data.state} /><span className="text-sm text-muted-foreground">{subscription.data.published_nodes}/{subscription.data.total_nodes} 个节点已发布</span></div>
    <p className="text-sm text-muted-foreground leading-relaxed my-5">订阅链接包含访问凭证，仅交付给对应用户。禁用用户后，节点撤销与链接吊销分别追踪。</p>
    {isDemo && <p className="text-xs text-muted-foreground mb-4">演示链接使用无效域名，不能用于代理连接。</p>}
    <Button variant="outline" disabled={subscription.data.state !== 'published' || user.status === 'disabled' || reveal.isPending} onClick={() => reveal.mutate()}><Link2 size={15} />{reveal.isPending ? '正在获取…' : '获取订阅链接'}</Button>
    {user.status !== 'disabled' && subscription.data.state === 'published' && links.map(link => <div key={link.label} className="subscription-box mt-4"><strong className="text-sm">{link.label}</strong><p className="break-all font-mono text-xs select-all mt-2">{link.url}</p><div className="flex gap-2 mt-3"><Button variant="outline" onClick={async () => { try { await navigator.clipboard.writeText(link.url); setCopied(link.label); setCopyError('') } catch { setCopyError('无法访问剪贴板，请选中链接手动复制。') } }}>{copied === link.label ? <Check /> : <Copy />}{copied === link.label ? '已复制' : `复制 ${link.label} 链接`}</Button><Button variant="ghost" onClick={() => { setLinks([]); setCopied('') }}>隐藏</Button></div></div>)}
    {reveal.error && <p className="field-error mt-3" role="alert">{reveal.error.message}</p>}{copyError && <p role="alert" className="field-error">{copyError}</p>}
  </>}</section>
}
export function UserDetail() {
  const { userId = '' } = useParams({ strict: false }), query = useQuery({ queryKey: ['user', userId], queryFn: ({ signal }) => api.user(userId, signal) })
  if (query.isPending || query.error) return <DataState loading={query.isPending} error={query.error} retry={() => query.refetch()} />
  const user = query.data
  return <><BackLink to="/users" label="返回用户列表" /><PageHeader title={user.display_name} description={user.note || '独立身份与节点授权'} action={<><StatusBadge status={user.status} />{user.status === 'disabled' ? <EnableUserButton user={user} /> : <><UserGrantsButton user={user} /><DisableUserButton user={user} /></>}</>} /><div className="detail-grid"><section className="panel p-6"><h2>用户信息</h2><dl className="mt-5"><KeyValue label="永久身份"><code className="break-all">{user.id}</code></KeyValue><KeyValue label="创建时间">{formatTime(user.created_at)}</KeyValue><KeyValue label="授权节点">{user.node_count} 个</KeyValue><KeyValue label="授权版本">revision {user.revision}</KeyValue></dl><div className="pt-5"><Link to="/usage" search={{ scope: 'managed', user: user.id }} className="workspace-link">查看用户流量<ArrowUpRight size={16} /></Link></div></section><SubscriptionPanel key={`${user.id}:${user.revision}`} user={user} /></div><p className="page-note">禁用会保留历史流量。重新启用必须使用新凭证，已撤销凭证不会恢复。</p></>
}
