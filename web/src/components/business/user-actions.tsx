import { useState } from 'react'
import { Controller, useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { LoaderCircle, Plus, ShieldCheck, ShieldOff } from 'lucide-react'
import { api, isDemo } from '@/api/client'
import type { User } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Checkbox } from '@/components/ui/checkbox'
import { DataState } from './common'
import { clearPendingRequest, submitUserRequest, usePendingRequest } from '@/lib/pending-request'
const createSchema = z.object({ display_name: z.string().trim().min(1, '请输入显示名称').max(40, '名称最多 40 个字符').refine(value => [...value].every(char => char.charCodeAt(0) >= 32), '名称不能包含控制字符'), note: z.string().max(160, '备注最多 160 个字符'), group_ids: z.array(z.string()).min(1, '至少选择一个节点组') })
export function CreateUserButton() {
  const [open, setOpen] = useState(false)
  const client = useQueryClient(), navigate = useNavigate()
  const pending = usePendingRequest()
  const form = useForm<z.infer<typeof createSchema>>({ resolver: zodResolver(createSchema), defaultValues: { display_name: '', note: '', group_ids: [] } })
  const groups = useQuery({ queryKey: ['groups'], queryFn: ({ signal }) => api.groups(signal), enabled: open })
  const mutation = useMutation({ mutationFn: async (data: z.infer<typeof createSchema>) => {
    return submitUserRequest('create_user', key => api.createUser(data, key))
  }, onSuccess: async result => {
    setOpen(false); form.reset()
    await client.invalidateQueries({ queryKey: ['users'] }); await client.invalidateQueries({ queryKey: ['overview'] })
    await navigate({ to: '/operations/$operationId', params: { operationId: result.operation_id } })
  } })
  return <><Button onClick={() => { mutation.reset(); setOpen(true) }}><Plus size={16} />添加用户</Button><Dialog open={open} onOpenChange={value => { if (!mutation.isPending) setOpen(value) }}><DialogContent className="sm:max-w-lg" showCloseButton={!mutation.isPending}><DialogHeader><DialogTitle>添加用户</DialogTitle><DialogDescription>分配独立身份和节点访问权限。{isDemo ? '当前操作仅作用于演示数据。' : '配置完成后可交付订阅。'}</DialogDescription></DialogHeader>
    <form onSubmit={form.handleSubmit(data => mutation.mutate(data))} className="space-y-5" noValidate>
      <div className="field"><Label htmlFor="display-name">显示名称</Label><Input id="display-name" autoComplete="off" placeholder="例如：陈同学" aria-invalid={!!form.formState.errors.display_name} aria-describedby="name-error" {...form.register('display_name')} /><p id="name-error" className="field-error" role="alert">{form.formState.errors.display_name?.message}</p></div>
      <fieldset><legend className="mb-2 text-sm font-medium">授权节点组</legend>{groups.isPending || groups.error ? <DataState loading={groups.isPending} error={groups.error} retry={() => groups.refetch()} /> : <Controller name="group_ids" control={form.control} render={({ field }) => <div className="group-options">{groups.data?.map(group => <label key={group.id} htmlFor={`grant-${group.id}`}><Checkbox id={`grant-${group.id}`} checked={field.value.includes(group.id)} onCheckedChange={checked => field.onChange(checked ? [...field.value, group.id] : field.value.filter(id => id !== group.id))} /><span>{group.name}<small>{group.node_ids.length} 个节点</small></span></label>)}</div>} />}<p className="field-error" role="alert">{form.formState.errors.group_ids?.message}</p></fieldset>
      <div className="field"><Label htmlFor="user-note">备注 <span className="text-muted-foreground font-normal">可选</span></Label><Textarea id="user-note" placeholder="记录用途，便于日后管理" {...form.register('note')} /><p className="field-error" role="alert">{form.formState.errors.note?.message}</p></div>
      {mutation.error && <p role="alert" className="field-error">{mutation.error.message}</p>}
      {pending && !mutation.isPending && <p role="status" className="text-sm text-muted-foreground">上次提交的结果尚未确认，请关闭弹窗后使用页面上的“找回操作”。</p>}
      <DialogFooter><Button variant="outline" type="button" disabled={mutation.isPending} onClick={() => setOpen(false)}>取消</Button><Button type="submit" disabled={mutation.isPending || !!pending || !groups.data?.length}>{mutation.isPending ? <LoaderCircle className="animate-spin" /> : <Plus />}提交配置</Button></DialogFooter>
    </form>
  </DialogContent></Dialog></>
}
export function DisableUserButton({ user }: { user: User }) {
  const [open, setOpen] = useState(false), [reason, setReason] = useState('管理员停用')
  const client = useQueryClient(), navigate = useNavigate()
  const mutation = useMutation({ mutationFn: () => submitUserRequest('disable_user', key => api.disableUser(user.id, { revision: user.revision, reason: reason.trim() }, key), user.id), onSuccess: async result => { setOpen(false); await client.invalidateQueries({ queryKey: ['users'] }); await client.invalidateQueries({ queryKey: ['user', user.id] }); await navigate({ to: '/operations/$operationId', params: { operationId: result.operation_id } }) } })
  return <><Button variant="destructive" disabled={user.status === 'disabled'} onClick={() => setOpen(true)}><ShieldOff size={15} />禁用用户</Button><Dialog open={open} onOpenChange={value => { if (!mutation.isPending) setOpen(value) }}><DialogContent showCloseButton={!mutation.isPending}><DialogHeader><DialogTitle>禁用「{user.display_name}」？</DialogTitle><DialogDescription>将撤销其 {user.node_count} 个节点的访问权限并吊销订阅。节点会重启，现有连接将中断并重连；失联节点恢复后优先处理。{isDemo && '本次仅演示，不会连接任何服务器。'}</DialogDescription></DialogHeader><div className="field"><Label htmlFor="disable-reason">停用原因</Label><Input id="disable-reason" value={reason} onChange={e => setReason(e.target.value)} maxLength={160} /></div>{mutation.error && <p role="alert" className="field-error">{mutation.error.message}</p>}<DialogFooter><Button variant="outline" disabled={mutation.isPending} onClick={() => setOpen(false)}>取消</Button><Button variant="destructive" disabled={mutation.isPending || !reason.trim()} onClick={() => mutation.mutate()}>{mutation.isPending && <LoaderCircle className="animate-spin" />}确认禁用</Button></DialogFooter></DialogContent></Dialog></>
}
export function EnableUserButton({ user }: { user: User }) {
  const [open, setOpen] = useState(false), [reason, setReason] = useState('管理员重新启用')
  const client = useQueryClient(), navigate = useNavigate()
  const pending = usePendingRequest()
  const groups = useQuery({ queryKey: ['groups'], queryFn: ({ signal }) => api.groups(signal), enabled: open })
  const selectedGroups = groups.data?.filter(group => user.group_ids.includes(group.id)) ?? []
  const nodeCount = new Set(selectedGroups.flatMap(group => group.node_ids)).size
  const mutation = useMutation({
    mutationFn: () => submitUserRequest('enable_user', key => api.enableUser(user.id, { revision: user.revision, reason: reason.trim() }, key), user.id),
    onSuccess: async result => {
      setOpen(false)
      await client.invalidateQueries({ queryKey: ['users'] })
      await client.invalidateQueries({ queryKey: ['user', user.id] })
      await client.invalidateQueries({ queryKey: ['subscription', user.id] })
      await client.invalidateQueries({ queryKey: ['overview'] })
      await navigate({ to: '/operations/$operationId', params: { operationId: result.operation_id } })
    },
  })
  return <><Button disabled={user.status !== 'disabled'} onClick={() => { mutation.reset(); setOpen(true) }}><ShieldCheck size={16} />重新启用</Button><Dialog open={open} onOpenChange={value => { if (!mutation.isPending) setOpen(value) }}><DialogContent showCloseButton={!mutation.isPending}><DialogHeader><DialogTitle>重新启用「{user.display_name}」？</DialogTitle><DialogDescription>保留用户身份和历史用量，生成新凭据与新订阅链接。不会重新授权旧凭据或恢复旧链接；失联节点上的旧凭据仍需等待撤销。启用后需要向用户交付新链接。节点会重启，现有连接将短暂中断并重连。{isDemo && '本次仅演示，不会连接任何服务器。'}</DialogDescription></DialogHeader>
    {groups.isPending || groups.error ? <DataState loading={groups.isPending} error={groups.error} retry={() => groups.refetch()} /> : <div className="notice"><p>按当前授权组恢复：{selectedGroups.map(group => group.name).join('、') || '无可用授权组'}，共 {nodeCount} 个节点。{nodeCount === 0 && '请先检查授权组的节点配置。'}</p></div>}
    <div className="field"><Label htmlFor="enable-reason">启用原因</Label><Input id="enable-reason" value={reason} onChange={e => setReason(e.target.value)} maxLength={160} /></div>
    {mutation.error && <p role="alert" className="field-error">{mutation.error.message}</p>}
    {pending && !mutation.isPending && <p role="status" className="text-sm text-muted-foreground">上次提交的结果尚未确认，请关闭弹窗后使用页面上的“找回操作”。</p>}
    <DialogFooter><Button variant="outline" disabled={mutation.isPending} onClick={() => setOpen(false)}>取消</Button><Button disabled={mutation.isPending || !!pending || !reason.trim() || !nodeCount || !!groups.error} onClick={() => mutation.mutate()}>{mutation.isPending ? <LoaderCircle className="animate-spin" /> : <ShieldCheck />}确认重新启用</Button></DialogFooter>
  </DialogContent></Dialog></>
}
export function PendingRequestNotice() {
  const pending = usePendingRequest()
  const navigate = useNavigate()
  const lookup = useMutation({ mutationFn: () => api.findRequest(pending!.key), onSuccess: result => { clearPendingRequest(); void navigate({ to: '/operations/$operationId', params: { operationId: result.operation_id } }) } })
  if (!pending) return null
  return <div className="notice notice-warning mb-5"><div><strong>上次提交的结果尚未确认</strong><p>{lookup.error?.message ?? '先找回原操作，避免重复提交变更。'}</p></div><Button variant="outline" disabled={lookup.isPending} onClick={() => lookup.mutate()}>找回操作</Button></div>
}
