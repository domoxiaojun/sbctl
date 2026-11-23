import { useEffect, useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useParams } from '@tanstack/react-router'
import { ArrowLeft, Check, ChevronRight, Copy, LoaderCircle, Search, Server, Sparkles, Terminal, RefreshCw, Pencil } from 'lucide-react'
import { api } from '@/api/client'
import type { Schema } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { BackLink, DataState, FilterSelect, KeyValue, PageHeader } from './common'
import { StatusBadge } from './status'
import { RestartNodeButton } from './node-tasks'
import { formatTime } from '@/lib/format'

type Draft = Schema['NodeDraft']
type Billing = Schema['NodeBilling']
type Job = Schema['NodeJob']
type RecordValue = Record<string, unknown>

const GIB = 1073741824n
const asRecord = (v: unknown): RecordValue => v && typeof v === 'object' ? v as RecordValue : {}
const text = (v: unknown) => typeof v === 'string' || typeof v === 'number' ? String(v) : ''
const emptyDraft = (): Draft => ({
  id: '',
  name: '',
  region: '',
  location: '',
  group_id: '',
  group_name: '',
  connection: { address: '', version: '1.14.4-sbctl.2' },
  container: '',
  config_dir: '/etc/sing-box',
  public_host: '',
  server_name: '',
})
const defaultBilling = (): Billing => ({
  quota_bytes: '0',
  reset_day: 1,
  accounting_mode: 'both',
  warn_percent: 80,
})

function bytesToGibText(bytes: string): string {
  try {
    const n = BigInt(bytes || '0')
    if (n <= 0n) return '0'
    return (Number(n * 100n / GIB) / 100).toString()
  } catch {
    return '0'
  }
}

function gibTextToBytes(gibText: string): string {
  const trimmed = gibText.trim()
  if (!trimmed || Number(trimmed) <= 0) return '0'
  const num = Number(trimmed)
  if (!Number.isFinite(num) || num < 0) return '0'
  return (BigInt(Math.round(num * 100)) * GIB / 100n).toString()
}

function useNodeJob(id: string) {
  return useQuery({
    queryKey: ['node-job', id],
    queryFn: ({ signal }) => api.nodeJob(id, signal),
    enabled: !!id,
    refetchInterval: query => ['queued', 'running'].includes(query.state.data?.state ?? '') ? 1000 : false,
  })
}

function Field({ name, label, value, onChange, required = false, hint, placeholder, readOnly = false, type = 'text' }: { name: string; label: string; value: string; onChange: (value: string) => void; required?: boolean; hint?: string; placeholder?: string; readOnly?: boolean; type?: string }) {
  return <div className="field"><Label htmlFor={name}>{label}{required && <span aria-hidden="true" className="text-destructive"> *</span>}</Label><Input id={name} type={type} value={value} onChange={e => onChange(e.target.value)} required={required} readOnly={readOnly} placeholder={placeholder} autoComplete="off" />{hint && <p className="text-xs text-muted-foreground leading-5">{hint}</p>}</div>
}

function BillingEditor({ billing, onChange }: { billing: Billing; onChange: (next: Billing) => void }) {
  const [gibInput, setGibInput] = useState(() => bytesToGibText(billing.quota_bytes))
  return <div className="space-y-4 rounded-xl border p-4">
    <div>
      <h3 className="text-sm font-medium">月度流量包与账单设置（可选）</h3>
      <p className="text-xs text-muted-foreground mt-1">填 0 表示不限制月流量；支持按每月固定账单日自动结算并触发预警。</p>
    </div>
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
      <Field
        name="billing-quota-gib"
        label="月流量上限 (GiB)"
        type="number"
        value={gibInput}
        onChange={val => {
          setGibInput(val)
          onChange({ ...billing, quota_bytes: gibTextToBytes(val) })
        }}
        placeholder="0（不限）或如 1024"
        hint="1024 GiB = 1 TiB"
      />
      <Field
        name="billing-reset-day"
        label="每月重置日 (1-31)"
        type="number"
        value={String(billing.reset_day)}
        onChange={val => onChange({ ...billing, reset_day: Math.min(31, Math.max(1, Number(val) || 1)) })}
        hint="大小月与二月自动对齐月末"
      />
      <div className="field">
        <Label>计费方向</Label>
        <FilterSelect
          label="选择计费方向"
          value={billing.accounting_mode}
          onChange={val => onChange({ ...billing, accounting_mode: val as Billing['accounting_mode'] })}
          options={[
            { value: 'both', label: '双向合计（上传+下载）' },
            { value: 'download_only', label: '仅计下载（节点出站）' },
            { value: 'upload_only', label: '仅计上传（节点入站）' },
            { value: 'max_direction', label: '单向取最大值' },
          ]}
        />
      </div>
      <Field
        name="billing-warn-percent"
        label="预警阈值 (%)"
        type="number"
        value={String(billing.warn_percent)}
        onChange={val => onChange({ ...billing, warn_percent: Math.min(100, Math.max(1, Number(val) || 80)) })}
        hint="达到百分比时触发告警"
      />
    </div>
  </div>
}

function JobState({ job, error, retry }: { job?: Job; error?: Error | null; retry?: () => void }) {
  if (error) return <p role="alert" className="field-error">{error.message}</p>
  if (!job) return null
  return <div className={`notice ${job.state === 'failed' ? 'notice-warning' : ''}`} role={job.state === 'failed' ? 'alert' : 'status'}><div><div className="flex items-center gap-2"><StatusBadge status={job.state} /><span className="text-sm">{job.state === 'running' ? '正在经内嵌控制通道与节点通信，请稍候…' : job.state === 'queued' ? '任务已保存，等待执行' : job.message}</span></div>{job.error_code && <p className="text-xs mt-2">{job.error_code === 'NODE_UNREACHABLE' ? '节点自研 sing-box 容器尚未连接主控：可先切换到“单容器快速登记”生成 compose.yaml 启动容器，确认 Tailscale 连通后再试。' : job.error_code === 'INSPECTION_CHANGED' || job.error_code === 'INSPECTION_EXPIRED' ? '节点配置已变化。返回上一步重新检查，再确认新的结果。' : '可修正节点配置后重试原任务，或返回上一步重新检查。'}</p>}</div>{job.state === 'failed' && retry && <Button variant="outline" onClick={retry}>重试原任务</Button>}</div>
}

function OnboardingHelp() {
  const [open, setOpen] = useState(false)
  const query = useQuery({ queryKey: ['node-onboarding'], queryFn: ({ signal }) => api.nodeOnboarding(signal), enabled: open })
  return <details className="panel p-5" open={open} onToggle={e => setOpen(e.currentTarget.open)}><summary className="cursor-pointer text-sm font-medium flex items-center gap-2"><Terminal size={16} />单容器零 SSH 部署说明</summary>{open && <div className="mt-5 space-y-3">{query.isPending || query.error ? <DataState loading={query.isPending} error={query.error} retry={() => query.refetch()} /> : <><ol className="list-decimal pl-5 space-y-2 text-sm text-muted-foreground">{query.data.instructions.map(item => <li key={item}>{item}</li>)}</ol><p className="text-xs text-muted-foreground">受管 VPS 不再需要开启 SSH 授权、不需要创建双 Linux 账号或安装宿主机客户端 Agent 二进制。所有热重载、配置快照与实时流量均由单容器内嵌监管器完成。</p></>}</div>}</details>
}

export function NodeSetupPage() {
  const { nodeId } = useParams({ strict: false }), edit = !!nodeId
  const saved = useQuery({ queryKey: ['node-config', nodeId], queryFn: ({ signal }) => api.nodeConfig(nodeId!, signal), enabled: edit })
  if (edit && (saved.isPending || saved.error)) return <DataState loading={saved.isPending} error={saved.error} retry={() => saved.refetch()} />
  return <NodeSetup key={`${nodeId ?? 'new'}-${saved.data?.version ?? 0}`} initial={saved.data} />
}

function NodeSetup({ initial }: { initial?: Schema['NodeConfiguration'] }) {
  const edit = !!initial
  const recoveryKey = `sbctl-node-management:${initial?.draft.id ?? 'new'}`
  const [recovered] = useState(() => {
    try {
      const raw = sessionStorage.getItem(recoveryKey)
      return raw ? JSON.parse(raw) as { mode?: 'enroll' | 'adopt'; controlUrl?: string; draft?: Draft; billing?: Billing; step?: number; jobId?: string; inspectionId?: string; desiredContainer?: string; desiredImage?: string; submitKey?: string; version?: number } : null
    } catch {
      return null
    }
  })

  const [mode, setMode] = useState<'enroll' | 'adopt'>(edit ? 'adopt' : (recovered?.mode ?? 'adopt'))
  const [draft, setDraft] = useState<Draft>(() => {
    const saved = recovered?.draft ?? initial?.draft ?? emptyDraft()
    return { ...saved, connection: { address: saved.connection.address, version: saved.connection.version } }
  })
  const [billing, setBilling] = useState<Billing>(recovered?.billing ?? initial?.billing ?? defaultBilling)
  const [controlUrl, setControlUrl] = useState(() => recovered?.controlUrl ?? location.origin)
  const [enrolled, setEnrolled] = useState<Schema['NodeEnrollResult'] | null>(null)
  const [copied, setCopied] = useState(false)

  const [step, setStep] = useState(recovered?.step ?? 1)
  const [jobId, setJobId] = useState(recovered?.jobId ?? '')
  const [inspectionId, setInspectionId] = useState(recovered?.inspectionId ?? '')
  const [confirmed, setConfirmed] = useState(false)
  const [error, setError] = useState('')
  const [desiredContainer, setDesiredContainer] = useState(recovered?.desiredContainer ?? '')
  const [desiredImage, setDesiredImage] = useState(recovered?.desiredImage ?? '')
  const [version] = useState(recovered?.version ?? initial?.version ?? 0)
  const [submitKey, setSubmitKey] = useState(() => recovered?.submitKey ?? crypto.randomUUID())

  const client = useQueryClient()
  const discovery = useQuery({ queryKey: ['node-discovery'], queryFn: ({ signal }) => api.nodeDiscovery(signal), enabled: step === 1 })
  const job = useNodeJob(jobId), inspection = useNodeJob(inspectionId)

  const enrollMutation = useMutation({
    mutationFn: () => api.enrollNode({ draft, billing, control_url: controlUrl }, submitKey),
    onSuccess: result => {
      setEnrolled(result)
      setSubmitKey(crypto.randomUUID())
      void client.invalidateQueries({ queryKey: ['nodes'] })
      void client.invalidateQueries({ queryKey: ['groups'] })
    },
  })

  const submit = useMutation({
    mutationFn: (kind: 'inspect' | 'configure') => api.submitNodeJob({ kind, draft, inspection_id: inspectionId, expected_version: version, desired_container: desiredContainer, desired_image: desiredImage, billing }, submitKey),
    onSuccess: (r, kind) => {
      setJobId(r.job_id)
      if (kind === 'inspect') setInspectionId(r.job_id)
      setStep(kind === 'inspect' ? 2 : 3)
      setSubmitKey(crypto.randomUUID())
    },
  })
  const retry = useMutation({ mutationFn: () => api.retryNodeJob(jobId), onSuccess: () => client.invalidateQueries({ queryKey: ['node-job', jobId] }) })
  const busy = submit.isPending || (job.data && ['queued', 'running'].includes(job.data.state))

  useEffect(() => {
    try {
      sessionStorage.setItem(recoveryKey, JSON.stringify({ mode, controlUrl, draft, billing, step, jobId, inspectionId, desiredContainer, desiredImage, submitKey, version }))
    } catch {
      /* best effort */
    }
  }, [recoveryKey, mode, controlUrl, draft, billing, step, jobId, inspectionId, desiredContainer, desiredImage, submitKey, version])

  useEffect(() => {
    if (job.data?.state === 'completed' && step === 3) {
      try { sessionStorage.removeItem(recoveryKey) } catch { /* ignore */ }
    }
  }, [job.data?.state, recoveryKey, step])

  const update = (key: keyof Draft, value: string) => { setDraft(d => ({ ...d, [key]: value })); setSubmitKey(crypto.randomUUID()) }
  const connection = (address: string) => { setDraft(d => ({ ...d, connection: { ...d.connection, address } })); setSubmitKey(crypto.randomUUID()) }
  const inspected = asRecord(inspection.data?.result)

  const handleEnroll = (e: FormEvent) => {
    e.preventDefault()
    setError('')
    if (!draft.connection.address.trim()) { setError('请输入节点 Tailscale 管理地址'); return }
    enrollMutation.mutate()
  }
  const review = (e: FormEvent) => {
    e.preventDefault()
    setError('')
    if (!draft.connection.address.trim()) { setError('请输入 Tailscale 管理地址'); return }
    submit.mutate('inspect')
  }
  const configure = (e: FormEvent) => {
    e.preventDefault()
    if (!confirmed) { setError('请确认接管范围与重连影响'); return }
    setError('')
    submit.mutate('configure')
  }

  useEffect(() => {
    if (job.data?.kind === 'configure' && job.data.state === 'completed') {
      void client.invalidateQueries({ queryKey: ['nodes'] })
      void client.invalidateQueries({ queryKey: ['node'] })
      void client.invalidateQueries({ queryKey: ['groups'] })
      void client.invalidateQueries({ queryKey: ['node-config', job.data.node_id], refetchType: 'none' })
    }
  }, [job.data, client])

  const copyCompose = async () => {
    if (!enrolled) return
    await navigator.clipboard.writeText(enrolled.compose_yaml)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return <>
    {initial ? <Link to="/nodes/$nodeId" params={{ nodeId: initial.draft.id }} search={{}} className="back-link"><ArrowLeft size={16} />返回节点管理</Link> : <BackLink to="/nodes" label="返回节点列表" />}
    <PageHeader title={edit ? '更新节点绑定' : '添加节点'} description="无需 SSH 与宿主机 Agent 二进制，单个自研 sing-box 容器即可完成注册、热重载与实时统计。" />

    {!edit && <div className="mb-5 flex flex-wrap gap-2" role="group" aria-label="接入方式">
      <Button type="button" variant={mode === 'adopt' ? 'secondary' : 'outline'} onClick={() => { setMode('adopt'); setError('') }}><Search size={15} />检查与接管在线容器</Button>
      <Button type="button" variant={mode === 'enroll' ? 'secondary' : 'outline'} onClick={() => { setMode('enroll'); setError('') }}><Sparkles size={15} />单容器快速登记（生成 compose.yaml）</Button>
    </div>}

    {step === 1 && <section className="panel p-6 mb-5">
      <div className="flex items-center justify-between gap-3 mb-4">
        <div><h2>从 Tailscale 发现</h2><p className="text-sm text-muted-foreground mt-1">选择主机，或在下方手动填写 Tailscale 管理地址。</p></div>
        <Button variant="outline" aria-label="刷新发现主机" disabled={discovery.isFetching} onClick={() => discovery.refetch()}><RefreshCw className={discovery.isFetching ? 'animate-spin' : ''} /></Button>
      </div>
      {discovery.isPending ? <p role="status" className="text-sm text-muted-foreground">正在读取主机…</p> : discovery.error ? <p role="alert" className="field-error">{discovery.error.message}，仍可手动添加。</p> : <>
        <p className="text-xs text-muted-foreground mb-4">{discovery.data?.message} · {formatTime(discovery.data?.sampled_at)}</p>
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">{discovery.data?.hosts.map(host => <button key={host.address} type="button" className="rounded-xl border p-4 text-left hover:bg-muted transition-colors disabled:opacity-60" disabled={!!host.node_id && host.node_id !== initial?.draft.id} onClick={() => { connection(host.address); if (!draft.name) update('name', host.name) }}><div className="flex gap-2 items-center"><Server size={16} /><strong className="text-sm truncate">{host.name}</strong></div><div className="text-xs font-mono text-muted-foreground mt-2">{host.address}</div><div className="text-xs mt-2">{host.node_id ? '已登记' : host.online ? 'Tailscale 在线' : 'Tailscale 离线'} · {host.os}</div></button>)}</div>
        {!discovery.data?.hosts.length && <p className="text-sm text-muted-foreground">暂无发现结果，请手动输入管理地址。</p>}
      </>}
    </section>}

    {mode === 'enroll' && !edit ? (
      <div className="space-y-5">
        <form className="panel p-6 space-y-5" onSubmit={handleEnroll}>
          <h2>节点标识与单容器参数</h2>
          <div className="grid gap-5 sm:grid-cols-2">
            <Field name="enroll-id" label="节点 ID" value={draft.id} onChange={v => update('id', v)} required placeholder="node_hk" hint="小写字母开头，可用数字、下划线和短横线。" />
            <Field name="enroll-name" label="节点名称" value={draft.name} onChange={v => update('name', v)} required placeholder="香港 01" />
            <Field name="enroll-region" label="地区代码" value={draft.region} onChange={v => update('region', v)} required placeholder="hk" />
            <Field name="enroll-location" label="位置" value={draft.location} onChange={v => update('location', v)} required placeholder="香港" />
            <Field name="enroll-group" label="节点组 ID" value={draft.group_id} onChange={v => update('group_id', v)} required placeholder="grp_hk" />
            <Field name="enroll-group-name" label="节点组名称" value={draft.group_name} onChange={v => update('group_name', v)} required placeholder="香港" />
            <Field name="enroll-address" label="Tailscale 管理地址" value={draft.connection.address} onChange={connection} required placeholder="100.70.0.10" />
            <Field name="enroll-public-host" label="公网地址或域名（可选）" value={draft.public_host} onChange={v => update('public_host', v)} placeholder="hk.example.com" />
            <Field name="enroll-config-dir" label="配置目录" value={draft.config_dir} onChange={v => update('config_dir', v)} required hint="映射到容器内 /etc/sing-box" />
            <Field name="enroll-control-url" label="主控访问地址 (Tailscale URL)" value={controlUrl} onChange={v => { setControlUrl(v); setSubmitKey(crypto.randomUUID()) }} required placeholder="http://100.64.0.1:8080" hint="供远端 sing-box 容器经 Tailscale 访问主控" />
          </div>
          <BillingEditor billing={billing} onChange={b => { setBilling(b); setSubmitKey(crypto.randomUUID()) }} />
          {(error || enrollMutation.error) && <p role="alert" className="field-error">{error || enrollMutation.error?.message}</p>}
          <div className="flex justify-end"><Button type="submit" disabled={enrollMutation.isPending}>{enrollMutation.isPending ? <LoaderCircle className="animate-spin" /> : <Sparkles />}登记节点并生成单容器 Compose</Button></div>
        </form>

        {enrolled && <section className="panel p-6 space-y-4 border-primary/40" aria-label="单容器部署配置">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h2 className="flex items-center gap-2"><Check size={18} className="text-primary" />节点 {enrolled.node_id} 已登记，请启动单容器</h2>
              <p className="text-xs text-muted-foreground mt-1">一次性注册码有效期至 {formatTime(enrolled.expires_at, true)}。请先将 SING_BOX_IMAGE 设为带内嵌 sbctl Agent 的镜像。容器首次启动后会换取持久凭据。</p>
            </div>
            <div className="flex gap-2">
              <Button type="button" variant="outline" onClick={() => { void copyCompose() }}><Copy size={14} />{copied ? '已复制' : '复制 compose.yaml'}</Button>
              <Button type="button" onClick={() => { setMode('adopt') }}>容器已启动，去检查接管 <ChevronRight size={15} /></Button>
            </div>
          </div>
          <Textarea readOnly value={enrolled.compose_yaml} rows={15} className="font-mono text-xs" />
          <div className="flex justify-end"><Link to="/nodes/$nodeId" params={{ nodeId: enrolled.node_id }} search={{}} className="entity-link">查看节点详情 <ChevronRight size={16} /></Link></div>
        </section>}

        <OnboardingHelp />
      </div>
    ) : (
      <>
        <div className="mb-6 flex flex-wrap items-center gap-3 text-sm" aria-label="接管步骤">
          {['连接主机', '检查与确认', '执行结果'].map((label, i) => <span key={label} className={`inline-flex items-center gap-2 ${step === i + 1 ? 'font-semibold text-foreground' : 'text-muted-foreground'}`}><span className="inline-flex size-7 items-center justify-center rounded-full border">{step > i + 1 ? <Check size={14} /> : i + 1}</span>{label}{i < 2 && <ChevronRight size={14} />}</span>)}
        </div>
        {step === 1 && <div className="space-y-5">
          <form className="panel p-6 space-y-5" onSubmit={review}>
            <h2>控制通道与容器</h2>
            <div className="grid gap-5 sm:grid-cols-2">
              <Field name="node-address" label="Tailscale 管理地址" value={draft.connection.address} onChange={connection} required placeholder="100.70.0.10" />
              <Field name="node-container" label="容器名称" value={draft.container} onChange={v => update('container', v)} placeholder="sing-box" hint="单容器默认名称为 sing-box。" />
              <Field name="node-config-dir" label="配置目录" value={draft.config_dir} onChange={v => update('config_dir', v)} required hint="支持 /etc/sing-box、/opt/sing-box、/opt/sbctl/managed 下的目录。" />
              <Field name="node-public-host" label="公网地址或域名（可选）" value={draft.public_host} onChange={v => update('public_host', v)} placeholder="hk.example.com" />
              <Field name="node-server-name" label="TLS 证书域名（可选）" value={draft.server_name ?? ''} onChange={v => update('server_name', v)} placeholder="hk.example.com" />
            </div>
            {(error || submit.error) && <p role="alert" className="field-error">{error || submit.error?.message}</p>}
            <div className="flex justify-end"><Button type="submit" disabled={submit.isPending}>{submit.isPending ? <LoaderCircle className="animate-spin" /> : <Search />}检查连接与容器</Button></div>
          </form>
          <OnboardingHelp />
        </div>}
        {step === 2 && <div className="space-y-5">
          <JobState job={job.data} error={job.error ?? retry.error} retry={() => retry.mutate()} />
          {inspection.data?.state === 'completed' && <form onSubmit={configure} className="panel p-6 space-y-6">
            <div>
              <h2>实际运行信息</h2>
              <dl className="mt-4">
                <KeyValue label="容器">{text(inspected.container)}</KeyValue>
                <KeyValue label="镜像标签"><code className="break-all">{text(inspected.image)}</code></KeyValue>
                <KeyValue label="实际镜像摘要"><code className="break-all text-xs">{text(inspected.engine_build_id)}</code></KeyValue>
                <KeyValue label="内核版本">{text(inspected.version)}</KeyValue>
                <KeyValue label="保留现有用户">{text(inspected.preserved_users)} 个协议凭据</KeyValue>
                <KeyValue label="入口">{Array.isArray(inspected.endpoints) ? inspected.endpoints.map(v => { const ep = asRecord(v); return `${text(ep.protocol)} :${text(ep.port)}` }).join(' / ') || '—' : '—'}</KeyValue>
              </dl>
            </div>
            <div className="grid gap-5 sm:grid-cols-2">
              <Field name="desired-container" label="接管后的容器名称" value={desiredContainer} onChange={v => { setDesiredContainer(v); setSubmitKey(crypto.randomUUID()); setConfirmed(false) }} hint="留空则保留当前容器名称。" placeholder={text(inspected.container)} />
              <Field name="desired-image" label="接管后的镜像引用" value={desiredImage} onChange={v => { setDesiredImage(v); setSubmitKey(crypto.randomUUID()); setConfirmed(false) }} hint="留空则保留当前自研镜像。" placeholder={text(inspected.image)} />
            </div>
            <div>
              <h2 className="mb-4">节点信息</h2>
              <div className="grid gap-5 sm:grid-cols-2">
                <Field name="node-id" readOnly={edit} label="节点 ID" value={draft.id} onChange={v => update('id', v)} required placeholder="node_hk" hint={edit ? '已有节点 ID 必须保持一致。' : '小写字母开头，可用数字、下划线和短横线。'} />
                <Field name="node-name" label="节点名称" value={draft.name} onChange={v => update('name', v)} required placeholder="香港 01" />
                <Field name="node-region" label="地区代码" value={draft.region} onChange={v => update('region', v)} required placeholder="hk" />
                <Field name="node-location" label="位置" value={draft.location} onChange={v => update('location', v)} required placeholder="香港" />
                <Field name="node-group" label="节点组 ID" value={draft.group_id} onChange={v => update('group_id', v)} required placeholder="grp_hk" />
                <Field name="node-group-name" label="节点组名称" value={draft.group_name} onChange={v => update('group_name', v)} required placeholder="香港" />
              </div>
            </div>
            <BillingEditor billing={billing} onChange={b => { setBilling(b); setSubmitKey(crypto.randomUUID()) }} />
            <div className="notice"><p>{text(inspected.impact)}</p></div>
            <label htmlFor="confirm-adoption" className="flex items-start gap-3 text-sm leading-6 cursor-pointer"><Checkbox id="confirm-adoption" checked={confirmed} onCheckedChange={value => setConfirmed(value === true)} className="mt-1" /><span>我确认接管此节点，并接受必要时的短暂重连。</span></label>
            {(error || submit.error) && <p role="alert" className="field-error">{error || submit.error?.message}</p>}
            <div className="flex justify-between gap-3"><Button variant="outline" type="button" disabled={!!busy} onClick={() => { setStep(1); setConfirmed(false); setJobId(''); setSubmitKey(crypto.randomUUID()) }}>返回修改</Button><Button type="submit" disabled={!!busy || !confirmed || inspected.can_adopt !== true}>{submit.isPending && <LoaderCircle className="animate-spin" />}{edit ? '确认更新绑定' : '确认添加节点'}</Button></div>
          </form>}
          {inspection.data?.state === 'failed' && <Button variant="outline" onClick={() => { setStep(1); setJobId(''); setSubmitKey(crypto.randomUUID()) }}>返回修改连接</Button>}
        </div>}
        {step === 3 && <section className="panel p-6 space-y-5"><h2>{job.data?.state === 'completed' ? '节点已接入' : '应用节点配置'}</h2><JobState job={job.data} error={job.error ?? retry.error} retry={() => retry.mutate()} />{job.data?.state === 'completed' && <><p className="text-sm text-muted-foreground">节点绑定已保存。单容器热重载与实时流量采集已就绪，可进入详情检查。</p><Link to="/nodes/$nodeId" params={{ nodeId: draft.id }} search={{}} className="entity-link">查看节点详情 <ChevronRight size={16} /></Link></>}{job.data?.state === 'failed' && <Button variant="outline" onClick={() => { setStep(1); setConfirmed(false); setJobId(''); setSubmitKey(crypto.randomUUID()) }}>重新检查配置</Button>}</section>}
      </>
    )}
  </>
}

export function NodeManagementPanel({ nodeId }: { nodeId: string }) {
  const client = useQueryClient(), navigate = useNavigate(), config = useQuery({ queryKey: ['node-config', nodeId], queryFn: ({ signal }) => api.nodeConfig(nodeId, signal) })
  const [jobId, setJobId] = useState(''), [editOpen, setEditOpen] = useState(false), [draft, setDraft] = useState<Draft | null>(null), [billing, setBilling] = useState<Billing>(defaultBilling)
  const [metadataKey, setMetadataKey] = useState(() => crypto.randomUUID()), [agentCode, setAgentCode] = useState('')
  const job = useNodeJob(jobId)
  const check = useMutation({ mutationFn: () => api.submitNodeJob({ kind: 'check', draft: config.data!.draft, inspection_id: '', expected_version: config.data!.version }, crypto.randomUUID()), onSuccess: r => { setJobId(r.job_id); void client.invalidateQueries({ queryKey: ['node-jobs', nodeId] }) } })
  const save = useMutation({
    mutationFn: () => api.updateNodeMetadata(nodeId, { draft: draft!, version: config.data!.version, billing }, metadataKey),
    onSuccess: async () => {
      setEditOpen(false)
      await client.invalidateQueries({ queryKey: ['node-config', nodeId] })
      await client.invalidateQueries({ queryKey: ['node', nodeId] })
      await client.invalidateQueries({ queryKey: ['nodes'] })
    },
  })
  const registerAgent = useMutation({ mutationFn: () => api.agentRegistration(nodeId, crypto.randomUUID()), onSuccess: result => setAgentCode(result.registration_code) })
  useEffect(() => { if (job.data && ['completed', 'failed'].includes(job.data.state)) void client.invalidateQueries({ queryKey: ['node-config', nodeId] }) }, [job.data, client, nodeId])
  if (config.isPending || config.error) return <DataState loading={config.isPending} error={config.error} retry={() => config.refetch()} />
  const binding = asRecord(config.data.binding), last = asRecord(config.data.last_check), busy = check.isPending || ['queued', 'running'].includes(job.data?.state ?? '')

  return <section className="panel p-5 sm:p-6 space-y-5" aria-label="sing-box 管理">
    <div className="flex flex-wrap items-start justify-between gap-4">
      <div><h2>sing-box 管理</h2><p className="text-sm text-muted-foreground mt-2">经单容器内嵌 Agent 执行无损热重载、健康检查，或编辑节点信息与月配额。</p></div>
      <div className="flex flex-wrap gap-2">
        <Button variant="outline" onClick={() => { setDraft(config.data.draft); setBilling(config.data.billing); setMetadataKey(crypto.randomUUID()); save.reset(); setEditOpen(true) }}><Pencil size={14} />编辑信息</Button>
        <Button variant="outline" disabled={busy} onClick={() => check.mutate()}>{busy ? <LoaderCircle className="animate-spin" /> : <RefreshCw />}检查节点</Button>
        <Button variant="outline" disabled={registerAgent.isPending} onClick={() => registerAgent.mutate()}>{registerAgent.isPending ? '正在生成…' : '重签容器注册码'}</Button>
        <RestartNodeButton config={config.data} />
        <Button onClick={() => navigate({ to: '/nodes/$nodeId/setup', params: { nodeId }, search: {} })}>更新绑定</Button>
      </div>
    </div>
    {registerAgent.error && <p role="alert" className="field-error">{registerAgent.error.message}</p>}
    {agentCode && <div className="notice space-y-2">
      <p className="text-sm font-medium">单容器一次性注册码（15 分钟有效）</p>
      <code className="block break-all text-xs">{agentCode}</code>
      <p className="text-xs text-muted-foreground">选择带内嵌 sbctl Agent 的镜像，设置以下环境变量完成注册：</p>
      <pre className="rounded bg-background/70 p-2 font-mono text-xs overflow-x-auto">{`SBCTL_CONTROL_URL=${location.origin}\nSBCTL_NODE_ID=${nodeId}\nSBCTL_REGISTRATION_CODE=${agentCode}`}</pre>
    </div>}
    <dl>
      <KeyValue label="容器名称">{text(binding.container) || '单容器（待首次绑定检查）'}</KeyValue>
      <KeyValue label="镜像"><code className="break-all">{text(binding.image) || '等待确认镜像'}</code></KeyValue>
      <KeyValue label="配置目录">{config.data.draft.config_dir}</KeyValue>
      <KeyValue label="节点组">{config.data.draft.group_name || config.data.draft.group_id || '—'}</KeyValue>
      <KeyValue label="最近检查">{formatTime(config.data.checked_at)}</KeyValue>
    </dl>
    <JobState job={job.data} error={job.error ?? check.error} />
    {Array.isArray(last.checks) && <details key={job.data?.state === 'completed' ? jobId : 'saved'} open={job.data?.state === 'completed' || undefined}><summary className="cursor-pointer text-sm min-h-11 flex items-center">查看检查结果（{last.checks.length} 项）</summary><div className="space-y-2"><p className="text-xs text-muted-foreground">{formatTime(text(last.checked_at))}{text(last.template_hash) && ` · 配置 ${text(last.template_hash).slice(0, 12)}`}</p>{last.checks.map((v, i) => { const c = asRecord(v); return <div key={i} className="rounded-lg bg-muted p-3 text-sm"><strong>{c.status === 'not_applicable' ? '不适用' : c.status === 'not_run' ? '未执行' : c.ok ? '通过' : '需处理'} · {text(c.name)}</strong><p className="text-muted-foreground mt-1">{text(c.message)}</p></div> })}</div></details>}
    <Dialog open={editOpen} onOpenChange={setEditOpen}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader><DialogTitle>编辑节点信息与月度配额</DialogTitle><DialogDescription>调整节点标签、所属分组及月度流量计费规则。</DialogDescription></DialogHeader>
        {draft && <form className="space-y-4" onSubmit={e => { e.preventDefault(); save.mutate() }}>
          <div className="grid gap-4 sm:grid-cols-2">
            {([['name', '节点名称'], ['region', '地区代码'], ['location', '位置'], ['group_id', '节点组 ID'], ['group_name', '节点组名称']] as const).map(([key, label]) => <Field key={key} name={`edit-${key}`} label={label} value={draft[key]} onChange={value => { setDraft(d => d ? { ...d, [key]: value } : d); setMetadataKey(crypto.randomUUID()) }} required />)}
          </div>
          <BillingEditor key={nodeId + String(config.data.version)} billing={billing} onChange={b => { setBilling(b); setMetadataKey(crypto.randomUUID()) }} />
          {save.error && <p role="alert" className="field-error">{save.error.message}</p>}
          <DialogFooter><Button type="button" variant="outline" onClick={() => setEditOpen(false)}>取消</Button><Button type="submit" disabled={save.isPending}>保存信息</Button></DialogFooter>
        </form>}
      </DialogContent>
    </Dialog>
  </section>
}
