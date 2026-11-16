import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { api, ApiError } from '@/api/client'
import type { Schema } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { formatTime } from '@/lib/format'

type View = Schema['NodeConfigView']
type Section = Schema['ConfigChange']['section']
const sections: [Section, string][] = [['route', '路由'], ['dns', 'DNS'], ['log', '日志'], ['ntp', 'NTP']]
const pretty = (value: unknown) => JSON.stringify(value, null, 2)

// 提交节点任务并等待结束。任务保存在服务端，页面关闭后仍可在“节点管理任务”中查看。
async function runNodeJob(input: Schema['NodeJobInput'], key: string) {
  const { job_id } = await api.submitNodeJob(input, key)
  for (;;) {
    const job = await api.nodeJob(job_id)
    if (job.state === 'completed') return job
    if (job.state === 'failed') throw new ApiError(job.error_code ?? 'NODE_JOB_FAILED', job.message ?? '节点任务失败，请在下方管理任务中查看。')
    await new Promise(resolve => setTimeout(resolve, 1000))
  }
}
function parseSection(text: string): { value?: Record<string, unknown>; problem?: string } {
  try {
    const value: unknown = JSON.parse(text)
    return value && typeof value === 'object' && !Array.isArray(value) ? { value: value as Record<string, unknown> } : { problem: '需要一个 JSON 对象，例如 {}。' }
  } catch (error) { return { problem: `JSON 格式无效：${error instanceof Error ? error.message : '无法解析'}` } }
}

export function NodeConfigSections({ nodeId }: { nodeId: string }) {
  const client = useQueryClient()
  const config = useQuery({ queryKey: ['node-config', nodeId], queryFn: ({ signal }) => api.nodeConfig(nodeId, signal) })
  const [view, setView] = useState<View | null>(null), [section, setSection] = useState<Section>('route'), [drafts, setDrafts] = useState<Partial<Record<Section, string>>>({})
  const [open, setOpen] = useState(false), [key, setKey] = useState(''), [saved, setSaved] = useState('')
  const base = (extra: Partial<Schema['NodeJobInput']>) => ({ draft: config.data!.draft, expected_version: config.data!.version, inspection_id: '', ...extra }) as Schema['NodeJobInput']
  const refresh = () => { for (const query of [['node-jobs', nodeId], ['node-config', nodeId], ['node', nodeId]]) void client.invalidateQueries({ queryKey: query }) }
  const loaded = (next: View) => { setView(next); setDrafts({}) }
  const read = useMutation({ gcTime: 0, mutationFn: async () => (await runNodeJob(base({ kind: 'config' }), crypto.randomUUID())).result as View, onSuccess: loaded, onSettled: refresh })
  const save = useMutation({ gcTime: 0, mutationFn: async (change: Schema['ConfigChange']) => {
    await runNodeJob(base({ kind: 'config_change', config_change: change }), key)
    // 保存后版本已变化；重新读取以取得下一次修改所需的版本。
    return (await runNodeJob(base({ kind: 'config' }), crypto.randomUUID())).result as View
  }, onSuccess: next => { loaded(next); setOpen(false); setSaved(label) }, onSettled: refresh })
  const label = sections.find(([id]) => id === section)![1], busy = read.isPending || save.isPending
  const original = view ? pretty(view.sections[section]) : '', text = drafts[section] ?? original, parsed = parseSection(text)
  const changed = (id: Section) => { if (!view || drafts[id] === undefined) return false; const draft = parseSection(drafts[id]); return !draft.value || JSON.stringify(draft.value) !== JSON.stringify(view.sections[id]) }
  const dirty = changed(section), error = config.error ?? read.error ?? (open ? null : save.error)
  return <section className="panel p-5 sm:p-6 mt-5 space-y-4" aria-labelledby="node-config-heading">
    <div className="flex flex-wrap items-start justify-between gap-3"><div><h2 id="node-config-heading">配置片段</h2><p className="text-sm text-muted-foreground mt-2">修改路由、DNS、日志和 NTP。保存时先预检，再重建 sing-box 容器并用探针验证，启动或验证失败会自动恢复原配置。</p></div><Button variant="outline" disabled={!config.data || busy} onClick={() => { setSaved(''); read.mutate() }}><RefreshCw size={16} />{read.isPending ? '正在读取…' : view ? '重新读取' : '读取配置'}</Button></div>
    {error && <p role="alert" className="field-error">{error.message}</p>}
    {saved && <p role="status" className="text-sm text-muted-foreground">{saved}配置已保存，sing-box 已重启并通过运行检查。</p>}
    {!view ? <p className="text-sm text-muted-foreground">先读取配置，再编辑片段。读取不会改动节点。</p> : <>
      <div role="group" aria-label="配置片段" className="flex flex-wrap gap-1">{sections.map(([id, name]) => <Button key={id} className="min-h-11" variant={section === id ? 'secondary' : 'ghost'} aria-pressed={section === id} onClick={() => setSection(id)}>{name}{changed(id) && <span aria-label="有未保存的修改"> •</span>}</Button>)}</div>
      <Textarea aria-label={`${label}配置 JSON`} aria-invalid={!!parsed.problem} className="font-mono text-xs min-h-72" spellCheck={false} value={text} onChange={e => { setSaved(''); setDrafts(old => ({ ...old, [section]: e.target.value })) }} />
      {parsed.problem && <p role="alert" className="field-error">{parsed.problem}</p>}
      <div className="flex flex-wrap gap-2"><Button className="min-h-11" disabled={!dirty || !parsed.value || busy} onClick={() => { save.reset(); setKey(crypto.randomUUID()); setOpen(true) }}>保存并重启</Button><Button className="min-h-11" variant="outline" disabled={!parsed.value} onClick={() => setDrafts(old => ({ ...old, [section]: pretty(parsed.value) }))}>格式化</Button><Button className="min-h-11" variant="ghost" disabled={drafts[section] === undefined} onClick={() => setDrafts(old => { const next = { ...old }; delete next[section]; return next })}>还原</Button></div>
      <p className="text-xs text-muted-foreground break-all">读取于 {formatTime(view.checked_at, true)}。可引用的入口：{view.inbound_tags.join('、') || '无'}；出站：{view.outbounds.map(o => `${o.tag || '未命名'}（${o.type}）`).join('、') || '无'}。入口在“协议入口”中管理；出站含凭据，暂不支持在此编辑。一次保存一个片段，其它片段的未保存修改会被丢弃。</p>
    </>}
    <Dialog open={open} onOpenChange={next => { if (!save.isPending) setOpen(next) }}><DialogContent><DialogHeader><DialogTitle>保存{label}配置并重启 sing-box？</DialogTitle><DialogDescription>将替换「{config.data?.draft.name}」的{label}配置。通过预检后会重建受管容器，该节点上的现有连接短暂中断；启动或探针验证失败时自动恢复原配置。凭据、入口和统计账本保留。</DialogDescription></DialogHeader>{save.error && <p role="alert" className="field-error">{save.error.message}</p>}<DialogFooter><Button variant="outline" disabled={save.isPending} onClick={() => setOpen(false)}>取消</Button><Button disabled={save.isPending || !view || !parsed.value} onClick={() => save.mutate({ base_hash: view!.hash, section, content: parsed.value! })}>{save.isPending ? '正在保存并验证…' : '确认保存并重启'}</Button></DialogFooter></DialogContent></Dialog>
  </section>
}
