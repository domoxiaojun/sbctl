import { useEffect, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import type { Schema } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { RefreshCw } from 'lucide-react'
import { useNodeCommand } from '@/lib/node-jobs'
import { DataState } from './common'
import { StatusBadge } from './status'
import { formatTime } from '@/lib/format'

export function RestartNodeButton({ config }: { config: Schema['NodeConfiguration'] }) {
  const [open, setOpen] = useState(false), [key, setKey] = useState(''), { submit, job, busy } = useNodeCommand(config.draft.id)
  return <><Button variant="outline" disabled={busy} onClick={() => { setKey(crypto.randomUUID()); setOpen(true) }}><RefreshCw size={14} />重启 sing-box</Button><Dialog open={open} onOpenChange={v => { if (!submit.isPending) setOpen(v) }}><DialogContent><DialogHeader><DialogTitle>重启 sing-box？</DialogTitle><DialogDescription>将重建「{config.draft.name}」的受管容器并检查运行结果，连接会短暂中断。现有配置、凭据和统计账本保留。</DialogDescription></DialogHeader>{submit.error && <p role="alert" className="field-error">{submit.error.message}</p>}<DialogFooter><Button variant="outline" disabled={submit.isPending} onClick={() => setOpen(false)}>取消</Button><Button disabled={submit.isPending} onClick={() => submit.mutate({ key, input: { kind: 'restart', draft: config.draft, expected_version: config.version, inspection_id: '' } }, { onSuccess: () => setOpen(false) })}>确认重启</Button></DialogFooter></DialogContent></Dialog>{job.data && job.data.kind === 'restart' && <span role="status" className="text-xs text-muted-foreground">重启任务：{job.data.state === 'completed' ? '已完成' : job.data.state === 'failed' ? '失败，见下方管理任务' : '正在执行'}</span>}</>
}
export function NodeTasks({ nodeId }: { nodeId: string }) {
  const client = useQueryClient()
  const query = useQuery({ queryKey: ['node-jobs', nodeId], queryFn: ({ signal }) => api.nodeJobs(nodeId, signal), refetchInterval: q => q.state.data?.some(j => ['queued', 'running'].includes(j.state)) ? 1000 : false })
  const refreshed = useRef('')
  useEffect(() => {
    if (!query.data) return
    const completed = query.data.filter(j => ['completed', 'failed'].includes(j.state)).map(j => `${j.id}:${j.state}:${j.updated_at}`).join('|')
    if (completed === refreshed.current) return
    refreshed.current = completed
    for (const key of [['node-config', nodeId], ['node', nodeId], ['nodes']]) void client.invalidateQueries({ queryKey: key })
  }, [query.data, client, nodeId])
  const retry = useMutation({ mutationFn: (id: string) => api.retryNodeJob(id), onSuccess: async () => { await client.invalidateQueries({ queryKey: ['node-jobs', nodeId] }); await client.invalidateQueries({ queryKey: ['node-job'] }) } })
  const names: Record<string, string> = { inspect: '检查连接与容器', configure: '更新节点绑定', check: '检查节点', protocols: '读取协议配置', protocol_change: '变更协议配置', config: '读取配置片段', config_change: '变更配置片段', restart: '重启 sing-box' }
  return <section className="panel p-5 mt-5" aria-label="节点管理任务"><div className="flex justify-between items-center gap-3"><div><h2>节点管理任务</h2><p className="text-xs text-muted-foreground mt-1">最近 20 项任务，刷新页面后可继续查看或重试。</p></div><Button variant="ghost" onClick={() => query.refetch()} disabled={query.isFetching}>刷新任务</Button></div>{retry.error && <p className="field-error" role="alert">{retry.error.message}</p>}{query.isPending || query.error ? <DataState loading={query.isPending} error={query.error} retry={() => query.refetch()} /> : !query.data.length ? <p className="text-sm text-muted-foreground mt-4">暂无管理任务。</p> : <ul className="mt-3 divide-y">{query.data.map(j => <li key={j.id} className="py-4"><div className="flex flex-wrap justify-between items-center gap-2"><strong className="text-sm">{names[j.kind] ?? j.kind}</strong><StatusBadge status={j.state} /></div><p className="text-sm text-muted-foreground mt-2">{j.message || '任务已保存，等待执行'}</p><div className="flex flex-wrap items-center justify-between gap-2 mt-2"><time className="text-xs text-muted-foreground">{formatTime(j.created_at)}</time>{j.state === 'failed' && <Button variant="outline" disabled={retry.isPending} onClick={() => retry.mutate(j.id)}>重试原任务</Button>}</div></li>)}</ul>}</section>
}
