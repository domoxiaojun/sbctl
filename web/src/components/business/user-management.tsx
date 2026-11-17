import { useId, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Archive, Pencil, RotateCcw, KeyRound, Trash2 } from 'lucide-react'
import type { User } from '@/api/types'
import { api } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { DataState } from './common'
import { formatTime } from '@/lib/format'

function useUserRefresh() {
  const client = useQueryClient()
  return async () => { await Promise.all(['users', 'user', 'user-audit', 'subscription', 'overview'].map(key => client.invalidateQueries({ queryKey: [key] }))) }
}
export function EditUserButton({ user }: { user: User }) {
  const [open, setOpen] = useState(false), [name, setName] = useState(user.display_name), [note, setNote] = useState(user.note), [key, setKey] = useState('')
  const refresh = useUserRefresh(), id = useId()
  const edit = useMutation({ mutationFn: () => api.editUser(user.id, { version: user.metadata_version, display_name: name.trim(), note }, key), onSuccess: async () => { setOpen(false); await refresh() } })
  return <><Button variant="outline" onClick={() => { setName(user.display_name); setNote(user.note); setKey(crypto.randomUUID()); edit.reset(); setOpen(true) }}><Pencil size={14} />编辑</Button><Dialog open={open} onOpenChange={v => { if (!edit.isPending) setOpen(v) }}><DialogContent><DialogHeader><DialogTitle>编辑用户资料</DialogTitle><DialogDescription>修改「{user.display_name}」的名称与备注，身份、凭据、授权和流量不变。</DialogDescription></DialogHeader><form className="space-y-4" onSubmit={e => { e.preventDefault(); edit.mutate() }}><div className="field"><Label htmlFor={`${id}-name`}>用户名称</Label><Input id={`${id}-name`} value={name} required maxLength={40} onChange={e => { setName(e.target.value); setKey(crypto.randomUUID()) }} /></div><div className="field"><Label htmlFor={`${id}-note`}>备注</Label><Textarea id={`${id}-note`} value={note} maxLength={160} onChange={e => { setNote(e.target.value); setKey(crypto.randomUUID()) }} /></div>{edit.error && <p role="alert" className="field-error">{edit.error.message}</p>}<DialogFooter><Button type="button" variant="outline" disabled={edit.isPending} onClick={() => setOpen(false)}>取消</Button><Button disabled={edit.isPending || !name.trim()} type="submit">{edit.isPending ? '正在保存…' : '保存资料'}</Button></DialogFooter></form></DialogContent></Dialog></>
}
export function ArchiveUsersButton({ users, restore = false, onDone }: { users: User[]; restore?: boolean; onDone?: () => void }) {
  const [open, setOpen] = useState(false), [key, setKey] = useState(''), refresh = useUserRefresh()
  const mutation = useMutation({ mutationFn: () => api.archiveUsers({ users: users.map(u => ({ id: u.id, version: u.metadata_version, revision: u.revision })), archived: !restore }, key), onSuccess: async () => { setOpen(false); onDone?.(); await refresh() } })
  const label = restore ? '恢复归档' : users.length > 1 ? `归档所选 ${users.length} 位` : '归档'
  return <><Button variant="outline" disabled={!users.length} onClick={() => { setKey(crypto.randomUUID()); mutation.reset(); setOpen(true) }}>{restore ? <RotateCcw size={14} /> : <Archive size={14} />}{label}</Button><Dialog open={open} onOpenChange={v => { if (!mutation.isPending) setOpen(v) }}><DialogContent><DialogHeader><DialogTitle>{restore ? '恢复归档用户' : '归档用户'}</DialogTitle><DialogDescription>{restore ? '恢复到用户列表，仍保持禁用状态。需要访问时再单独重新启用。' : '从默认列表收起已禁用且撤销完成的用户，保留身份和操作记录。可在“已归档”中恢复。'}</DialogDescription></DialogHeader><ul className="max-h-60 overflow-auto text-sm space-y-2">{users.map(u => <li key={u.id}>{u.display_name}<code className="block text-xs text-muted-foreground break-all">{u.id}</code></li>)}</ul>{mutation.error && <p role="alert" className="field-error">{mutation.error.message}</p>}<DialogFooter><Button variant="outline" disabled={mutation.isPending} onClick={() => setOpen(false)}>取消</Button><Button disabled={mutation.isPending} onClick={() => mutation.mutate()}>{mutation.isPending ? '正在保存…' : restore ? '确认恢复' : '确认归档'}</Button></DialogFooter></DialogContent></Dialog></>
}
export function DeleteUsersButton({ users, onDone }: { users: User[]; onDone?: () => void | Promise<unknown> }) {
  const [open, setOpen] = useState(false), [confirmation, setConfirmation] = useState(''), [key, setKey] = useState('')
  const refresh = useUserRefresh(), id = useId(), client = useQueryClient()
  const mutation = useMutation({
    mutationFn: () => api.deleteUsers({ users: users.map(u => ({ id: u.id, version: u.metadata_version, revision: u.revision })) }, key),
    onSuccess: async () => {
      setOpen(false)
      await onDone?.()
      users.forEach(u => client.removeQueries({ queryKey: ['user', u.id], exact: true }))
      await Promise.all([refresh(), ...['operations', 'operation', 'traffic'].map(k => client.invalidateQueries({ queryKey: [k] }))])
    },
  })
  return <><Button variant="outline" className="text-destructive" disabled={!users.length || users.some(u => !u.archived)} onClick={() => { setConfirmation(''); setKey(crypto.randomUUID()); mutation.reset(); setOpen(true) }}><Trash2 size={14} />{users.length > 1 ? `删除所选 ${users.length} 位` : '删除用户'}</Button><Dialog open={open} onOpenChange={v => { if (!mutation.isPending) setOpen(v) }}><DialogContent><DialogHeader><DialogTitle>删除已归档用户？</DialogTitle><DialogDescription>删除后不能恢复，用户资料、组关联、已撤销凭据和订阅记录将移除。操作记录和撤销指纹保留；不会重启节点。</DialogDescription></DialogHeader><ul className="max-h-48 overflow-auto text-sm space-y-2">{users.map(u => <li key={u.id}>{u.display_name}<code className="block text-xs text-muted-foreground break-all">{u.id}</code></li>)}</ul><div className="field"><Label htmlFor={id}>输入“删除”确认</Label><Input id={id} value={confirmation} onChange={e => setConfirmation(e.target.value)} autoComplete="off" disabled={mutation.isPending} /></div>{mutation.error && <p role="alert" className="field-error">{mutation.error.message}</p>}<DialogFooter><Button variant="outline" disabled={mutation.isPending} onClick={() => setOpen(false)}>取消</Button><Button variant="destructive" disabled={mutation.isPending || confirmation !== '删除'} onClick={() => mutation.mutate()}>{mutation.isPending ? '正在删除…' : '确认删除'}</Button></DialogFooter></DialogContent></Dialog></>
}
export function ResetSubscriptionButton({ user }: { user: User }) {
  const [open, setOpen] = useState(false), [key, setKey] = useState(''), [reason, setReason] = useState('管理员重置订阅链接'), refresh = useUserRefresh(), id = useId()
  const mutation = useMutation({ mutationFn: () => api.resetSubscription(user.id, { revision: user.revision, generation: user.subscription_generation, reason }, key), onSuccess: async () => { setOpen(false); await refresh() } })
  return <><Button variant="outline" disabled={user.status !== 'active' || user.subscription_state !== 'published'} onClick={() => { setKey(crypto.randomUUID()); mutation.reset(); setOpen(true) }}><KeyRound size={14} />重置订阅链接</Button><Dialog open={open} onOpenChange={v => { if (!mutation.isPending) setOpen(v) }}><DialogContent><DialogHeader><DialogTitle>重置订阅链接？</DialogTitle><DialogDescription>旧链接立即吊销，新链接发布后需要重新交付给用户。代理凭据和已有连接不变，不会重启节点；已经下载的代理配置仍可使用。</DialogDescription></DialogHeader><div className="field"><Label htmlFor={id}>重置原因</Label><Input id={id} value={reason} maxLength={160} onChange={e => { setReason(e.target.value); setKey(crypto.randomUUID()) }} /></div>{mutation.error && <p role="alert" className="field-error">{mutation.error.message}</p>}<DialogFooter><Button variant="outline" disabled={mutation.isPending} onClick={() => setOpen(false)}>取消</Button><Button disabled={mutation.isPending || !reason.trim()} onClick={() => mutation.mutate()}>确认重置</Button></DialogFooter></DialogContent></Dialog></>
}
export function UserGroupNames({ user }: { user: User }) {
  const groups = useQuery({ queryKey: ['groups'], queryFn: ({ signal }) => api.groups(signal), staleTime: 60_000 })
  return <span>{user.group_ids.map(id => groups.data?.find(g => g.id === id)?.name ?? id).join('、') || '无授权组'}</span>
}
export function UserAudit({ user }: { user: User }) {
  const query = useQuery({ queryKey: ['user-audit', user.id], queryFn: ({ signal }) => api.userAudit(user.id, signal) })
  const labels: Record<string, string> = { create_user: '创建用户', edit_user: '编辑资料', archive_user: '归档用户', restore_user: '恢复归档', reset_subscription: '重置订阅链接', subscription_link: '获取订阅链接', disable_user: '禁用用户', enable_user: '重新启用', update_grants: '调整授权' }
  return <section className="panel p-5 mt-5"><h2>管理记录</h2>{query.isPending || query.error ? <DataState loading={query.isPending} error={query.error} retry={() => query.refetch()} /> : query.data.length ? <ul className="divide-y mt-3">{query.data.map((row, i) => <li className="py-3 text-sm" key={i}><div className="flex flex-wrap justify-between gap-2"><strong>{labels[row.action] ?? '管理操作'}</strong><time className="text-xs text-muted-foreground">{formatTime(row.created_at)}</time></div><p className="text-muted-foreground mt-1">{row.summary}</p></li>)}</ul> : <p className="text-sm text-muted-foreground mt-3">暂无管理记录。</p>}</section>
}
