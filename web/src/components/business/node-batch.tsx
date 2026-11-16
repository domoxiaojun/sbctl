import { createContext, useCallback, useContext, useEffect, useId, useMemo, useRef, useState, type ReactNode } from 'react'
import { queryOptions, skipToken, useQueries, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { ListChecks, RefreshCw } from 'lucide-react'
import { api, ApiError } from '@/api/client'
import type { Node, Schema } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { StatusBadge } from './status'

type Kind = 'check' | 'restart'
type Job = Schema['NodeJob']
type Row = { id: string; name: string; jobId?: string; error?: string }
type Run = { id: string; kind: Kind; rows: Row[] }
type Outcome = { mark: keyof typeof marks; note: string; tone?: 'bad' | 'warn' }
// The controller keeps at most 20 node jobs queued or running, so a larger batch could never be accepted whole.
const limit = 20
// The run is kept in the query cache: it survives a visit to a node's detail page and is dropped on logout with every other query.
const runKey = ['node-batch']
const titles = { check: '批量检查', restart: '批量重启' }
// StatusBadge prints a status it does not know as is, in its in-progress style; its own label for queued is “已接收”.
const marks = { submitting: '提交中', queued: '排队', running: 'running', completed: 'completed', failed: 'failed' }
// The submit buttons stay focusable while they are unavailable, so :disabled styles do not reach them.
const dimmed = 'aria-disabled:pointer-events-none aria-disabled:opacity-50'
const asRecord = (v: unknown): Record<string, unknown> => v && typeof v === 'object' ? v as Record<string, unknown> : {}
const text = (v: unknown) => typeof v === 'string' || typeof v === 'number' ? String(v) : ''
const jobQuery = (id: string) => queryOptions({ queryKey: ['node-job', id], queryFn: ({ signal }) => api.nodeJob(id, signal), refetchInterval: q => ['completed', 'failed'].includes(q.state.data?.state ?? '') ? false : 1000 })
function outcome(kind: Kind, row: Row, job?: Job, unread = false): Outcome {
  if (row.error) return { mark: 'failed', note: row.error, tone: 'bad' }
  if (!row.jobId) return { mark: 'submitting', note: '正在读取节点配置并提交任务…' }
  const state = job?.state ?? 'queued'
  if (state === 'failed') return { mark: state, note: job?.message || '任务失败，请进入节点详情查看。', tone: 'bad' }
  if (state !== 'completed') return unread ? { mark: state, note: '暂时无法读取任务状态，正在重试…', tone: 'warn' } : { mark: state, note: state === 'running' ? '正在与节点通信，请稍候…' : job?.message || '任务已保存，等待执行' }
  // A check job completes even when the node fails its checks, so the finding has to come from the result.
  const result = asRecord(job?.result), checks = Array.isArray(result.checks) ? result.checks.map(asRecord) : [], problems = checks.filter(c => !c.ok)
  if (kind === 'check' && (problems.length || result.ready === false)) return { mark: state, note: problems.length ? `需处理：${problems.map(c => [text(c.name), text(c.message)].filter(Boolean).join('：')).join('；')}` : '节点未就绪，请进入节点详情查看检查结果。', tone: 'warn' }
  return { mark: state, note: kind === 'check' && checks.length ? `${checks.length} 项检查通过` : job?.message || '任务已完成' }
}
const Selection = createContext<{ ids: string[]; toggle: (id: string, checked: boolean) => void }>({ ids: [], toggle: () => {} })
export function NodeSelectBox({ node }: { node: Node }) {
  const { ids, toggle } = useContext(Selection)
  return <Checkbox aria-label={`选择 ${node.name} ${node.id}`} checked={ids.includes(node.id)} onCheckedChange={v => toggle(node.id, v === true)} />
}
export function NodeBatch({ nodes, scope, children }: { nodes: Node[]; scope: string; children: ReactNode }) {
  const client = useQueryClient(), allId = useId()
  const [selected, setSelected] = useState<string[]>([]), [listed, setListed] = useState(scope), [confirming, setConfirming] = useState(false)
  // Paging or filtering shows another list; a selection made on the previous one must neither follow nor come back.
  if (listed !== scope) { setListed(scope); setSelected([]) }
  const toggle = useCallback((id: string, checked: boolean) => setSelected(old => checked ? [...old, id] : old.filter(v => v !== id)), [])
  const selection = useMemo(() => ({ ids: selected, toggle }), [selected, toggle])
  const chosen = nodes.filter(n => selected.includes(n.id)), over = chosen.length > limit
  // The dialog confirms the live selection. Once nothing is left it closes for good instead of reopening with the next selection.
  if (confirming && !chosen.length) setConfirming(false)
  const run = useQuery<Run | null>({ queryKey: runKey, queryFn: skipToken }).data ?? null
  const submitted = run?.rows.filter(r => r.jobId) ?? []
  const jobs = useQueries({ queries: submitted.map(r => jobQuery(r.jobId!)) })
  const rows = run ? run.rows.map(row => { const job = jobs[submitted.indexOf(row)]; return { ...row, ...outcome(run.kind, row, job?.data, !!job?.error) } }) : []
  const ended = rows.filter(r => r.mark === 'completed' || r.mark === 'failed').length, failed = rows.filter(r => r.mark === 'failed').length
  const busy = ended < rows.length, blocked = busy || over || !chosen.length
  const wasBusy = useRef(false)
  useEffect(() => {
    if (busy) { wasBusy.current = true; return }
    if (!wasBusy.current) return
    wasBusy.current = false
    // Besides the list, each node that got a job has a changed check result, version or task history.
    for (const key of [['nodes'], ...(run?.rows ?? []).filter(r => r.jobId).flatMap(r => [['node', r.id], ['node-config', r.id], ['node-jobs', r.id]])]) void client.invalidateQueries({ queryKey: key })
  }, [busy, run, client])
  const start = (kind: Kind) => {
    if (blocked) return
    const id = crypto.randomUUID()
    const record = (nodeId: string, result: Partial<Row>) => client.setQueryData<Run | null>(runKey, old => old?.id === id ? { ...old, rows: old.rows.map(r => r.id === nodeId ? { ...r, ...result } : r) } : old)
    client.setQueryData<Run>(runKey, { id, kind, rows: chosen.map(n => ({ id: n.id, name: n.name })) })
    // Every node is submitted on its own, so one that is busy, stale or unreachable leaves the others untouched.
    for (const n of chosen) void (async () => {
      let sent = false
      try {
        const config = await api.nodeConfig(n.id)
        sent = true
        record(n.id, { jobId: (await api.submitNodeJob({ kind, draft: config.draft, inspection_id: '', expected_version: config.version }, crypto.randomUUID())).job_id })
      } catch (error) {
        // A write that never answered may still have been saved; say so rather than invite a second restart.
        record(n.id, { error: sent && error instanceof ApiError && error.code === 'API_UNAVAILABLE' ? '提交结果未确认，任务可能已经保存。请先在节点详情查看管理任务，再决定是否重试。' : error instanceof Error && error.message ? error.message : '提交失败，请稍后重试。' })
      }
    })()
  }
  return <Selection value={selection}>
    <div className="border-b px-5 py-3 flex flex-wrap items-center gap-x-4 gap-y-2">
      <label htmlFor={allId} className="flex min-h-10 items-center gap-3 text-sm cursor-pointer"><Checkbox id={allId} aria-label="全选本页节点" disabled={!nodes.length} checked={nodes.length > 0 && chosen.length === nodes.length} onCheckedChange={v => setSelected(v === true ? nodes.map(n => n.id) : [])} />全选本页</label>
      {chosen.length > 0 ? <div role="group" aria-label="批量节点操作" className="flex flex-wrap items-center gap-2"><span role="status" className="mr-1 text-sm">已选 <strong className="tabular-nums">{chosen.length}</strong> 台</span><Button variant="outline" aria-label="批量检查所选节点" disabled={blocked} focusableWhenDisabled className={dimmed} onClick={() => start('check')}><ListChecks />批量检查</Button><Button variant="outline" aria-label="批量重启所选节点" disabled={blocked} focusableWhenDisabled className={dimmed} onClick={() => setConfirming(true)}><RefreshCw />批量重启</Button><Button variant="ghost" aria-label="取消选择节点" onClick={() => setSelected([])}>取消选择</Button></div> : <span className="text-xs text-muted-foreground">勾选节点后可批量检查或重启。</span>}
      {over ? <p role="alert" className="field-error basis-full">最多同时选择 {limit} 台节点，请减少选择后再操作。</p> : busy && chosen.length > 0 && <p className="basis-full text-xs text-muted-foreground">批量任务进行中，全部结束后可再次提交。</p>}
    </div>
    {run && <section aria-label="批量任务进度" className="border-b p-5">
      <div className="flex items-start justify-between gap-3"><div className="min-w-0 flex-1"><h2>{titles[run.kind]}进度</h2><p className="text-xs text-muted-foreground mt-1"><span role="status">{busy ? `正在执行，${ended}/${rows.length} 台已结束。` : `已全部结束：${ended - failed} 台完成${failed ? `，${failed} 台失败` : ''}。`}</span>{busy && '已保存的任务不会因离开页面而取消，可在节点详情继续查看。'}</p></div><Button variant="ghost" aria-label="清除批量任务结果" disabled={busy} onClick={() => client.setQueryData(runKey, null)}>清除结果</Button></div>
      <ul className="mt-3 divide-y">{rows.map(r => <li key={r.id} className="py-3 flex flex-wrap items-center gap-x-3 gap-y-2 text-sm"><Link to="/nodes/$nodeId" params={{ nodeId: r.id }} search={{}} className="min-w-0 break-all font-medium hover:text-primary sm:w-44">{r.name}</Link><span className="flex sm:w-20"><StatusBadge status={marks[r.mark]} /></span><span className={`basis-full min-w-0 break-words sm:basis-0 sm:grow ${r.tone === 'bad' ? 'text-destructive' : r.tone === 'warn' ? 'text-warning' : 'text-muted-foreground'}`}>{r.note}</span></li>)}</ul>
    </section>}
    <Dialog open={confirming} onOpenChange={setConfirming}><DialogContent><DialogHeader><DialogTitle>批量重启 {chosen.length} 台节点？</DialogTitle><DialogDescription>将重建以下节点的受管容器并检查运行结果，每台节点上的现有连接会短暂中断。主控会分批并行执行，默认最多 3 台同时进行。现有配置、凭据和统计账本保留。</DialogDescription></DialogHeader><ul className="max-h-60 overflow-auto text-sm space-y-2">{chosen.map(n => <li key={n.id}>{n.name}<code className="block text-xs text-muted-foreground break-all">{n.id}</code></li>)}</ul><DialogFooter><Button variant="outline" onClick={() => setConfirming(false)}>取消</Button><Button disabled={blocked} onClick={() => { setConfirming(false); start('restart') }}>确认重启</Button></DialogFooter></DialogContent></Dialog>
    {children}
  </Selection>
}
