import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { ArrowLeft, ListFilter, LoaderCircle, ShieldCheck } from 'lucide-react'
import { api, ApiError, isDemo } from '@/api/client'
import type { User } from '@/api/types'
import { submitUserRequest, usePendingRequest } from '@/lib/pending-request'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { DataState } from './common'

export function UserGrantsButton({ user }: { user: User }) {
  const [open, setOpen] = useState(false), [selected, setSelected] = useState<string[]>([])
  const [reason, setReason] = useState('调整使用区域')
  const client = useQueryClient(), navigate = useNavigate(), pending = usePendingRequest()
  const groups = useQuery({ queryKey: ['groups'], queryFn: ({ signal }) => api.groups(signal), enabled: open })
  const preview = useMutation({ mutationFn: () => api.previewGrants(user.id, { revision: user.revision, group_ids: selected }), onError: error => {
    if (error instanceof ApiError && error.code === 'STALE_REVISION') void client.invalidateQueries({ queryKey: ['user', user.id] })
  } })
  const save = useMutation({
    mutationFn: () => {
      const impact = preview.data!
      return submitUserRequest('update_grants', key => api.updateGrants(user.id, { revision: impact.revision, group_ids: impact.group_ids, preview_hash: impact.preview_hash, reason: reason.trim() }, key), user.id)
    },
    onSuccess: async result => {
      setOpen(false)
      await Promise.all(['users', 'user', 'subscription', 'overview', 'operations', 'nodes'].map(key => client.invalidateQueries({ queryKey: [key] })))
      await navigate({ to: '/operations/$operationId', params: { operationId: result.operation_id } })
    },
    onError: error => {
      if (error instanceof ApiError && ['STALE_REVISION', 'GRANT_PREVIEW_CHANGED', 'NODE_BUSY', 'USER_DISABLED'].includes(error.code)) {
        preview.reset()
        void client.invalidateQueries({ queryKey: ['user', user.id] })
        void client.invalidateQueries({ queryKey: ['groups'] })
      }
    },
  })
  const busy = preview.isPending || save.isPending
  return <>
    <Button variant="outline" disabled={user.status === 'disabled'} onClick={() => { setSelected(user.group_ids); preview.reset(); save.reset(); setOpen(true) }}><ListFilter size={16} />调整授权</Button>
    <Dialog open={open} onOpenChange={value => { if (!busy) setOpen(value) }}>
      <DialogContent className="sm:max-w-xl" showCloseButton={!busy}>
        <DialogHeader>
          <DialogTitle>调整「{user.display_name}」的授权</DialogTitle>
          <DialogDescription>{preview.data ? '第 2 步 · 确认节点影响' : '第 1 步 · 选择授权节点组'}。多个组按节点并集授权，保留用户身份和历史用量。{isDemo && '当前操作仅作用于演示数据。'}</DialogDescription>
        </DialogHeader>
        {!preview.data ? <>
          <fieldset disabled={busy}>
            <legend className="mb-3 font-medium">授权节点组</legend>
            {groups.isPending || groups.error ? <DataState loading={groups.isPending} error={groups.error} retry={() => groups.refetch()} /> : <div className="group-options">
              {groups.data.map(group => <label key={group.id} htmlFor={`edit-grant-${group.id}`}>
                <Checkbox id={`edit-grant-${group.id}`} checked={selected.includes(group.id)} onCheckedChange={checked => { setSelected(previous => checked ? [...previous, group.id] : previous.filter(id => id !== group.id)); save.reset() }} />
                <span>{group.name}<small>{group.node_ids.length} 个节点</small></span>
              </label>)}
            </div>}
          </fieldset>
          <p className="text-sm text-muted-foreground">至少保留一个有可管理节点的组。停止全部访问请使用“禁用用户”。</p>
        </> : <>
          <div className="space-y-3" aria-label="授权影响预览">
            {([['added', '新增授权'], ['retained', '保留授权'], ['removed', '撤销授权']] as const).map(([key, label]) => <section key={key} className="rounded-lg border p-4">
              <h3 className="flex justify-between gap-3 font-medium"><span>{label}</span><span className="numeric">{preview.data[key].length} 个节点</span></h3>
              <p className="mt-2 break-words text-sm text-muted-foreground">{preview.data[key].map(node => node.name).join('、') || '无'}</p>
            </section>)}
          </div>
          <p className="text-sm text-muted-foreground leading-relaxed">保留节点沿用现有凭据。新增和撤销可能重启节点，连接会短暂中断；失联节点恢复后继续撤销。订阅链接保持不变，内容仅发布仍授权且验证通过的节点，更新期间可能暂时不可用。</p>
          {!preview.data.changed && <p role="status" className="notice">授权范围没有变化，可以返回重新选择。</p>}
          <div className="field"><Label htmlFor="grant-reason">调整原因</Label><Input id="grant-reason" value={reason} onChange={e => setReason(e.target.value)} maxLength={160} disabled={save.isPending} /></div>
        </>}
        {(preview.error || save.error) && <p role="alert" className="field-error">{(save.error || preview.error)?.message}</p>}
        {pending && !save.isPending && <p role="status" className="text-sm text-muted-foreground">上次提交的结果尚未确认，请关闭弹窗后使用页面上的“找回操作”。</p>}
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={() => { if (preview.data) { preview.reset(); save.reset() } else setOpen(false) }}>{preview.data && <ArrowLeft size={15} />}{preview.data ? '返回选择' : '取消'}</Button>
          {preview.data ? <Button disabled={save.isPending || !!pending || !reason.trim() || !preview.data.changed} onClick={() => save.mutate()}>{save.isPending ? <LoaderCircle className="animate-spin" /> : <ShieldCheck />}确认调整授权</Button>
            : <Button disabled={busy || !!pending || !selected.length || groups.isPending || !!groups.error} onClick={() => { save.reset(); preview.mutate() }}>{preview.isPending && <LoaderCircle className="animate-spin" />}预览授权影响</Button>}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </>
}
