import { Link } from '@tanstack/react-router'
import type { Schema } from '@/api/types'
import { DataFreshness } from './status'
import { formatBytes, formatTime, totalBytes } from '@/lib/format'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'

export function ProtocolTraffic({ nodes }: { nodes: Schema['Node'][] }) {
  if (!nodes.length) return <p className="panel p-6 text-muted-foreground">尚未登记节点，添加节点并完成统计接管后显示数据。</p>
  return <div className="space-y-5">{nodes.map(node => {
    const inbounds = node.inbounds ?? [], active = inbounds.filter(row => row.active)
    const protocols = new Map<string, { upload_bytes: string; download_bytes: string }>()
    for (const row of inbounds) {
      const prior = protocols.get(row.protocol) ?? { upload_bytes: '0', download_bytes: '0' }
      protocols.set(row.protocol, { upload_bytes: (BigInt(prior.upload_bytes) + BigInt(row.upload_bytes)).toString(), download_bytes: (BigInt(prior.download_bytes) + BigInt(row.download_bytes)).toString() })
    }
    return <section className="panel p-5 sm:p-6 space-y-5" key={node.id}>
      <div className="flex flex-wrap justify-between gap-3"><div><h2><Link to="/nodes/$nodeId" params={{ nodeId: node.id }} className="hover:text-primary">{node.name}</Link></h2><p className="mt-2 text-sm text-muted-foreground">{active.length} / {node.total_inbounds || '—'} 个当前入口已采集 · 上传为客户端→节点，下载为节点→客户端</p></div>{inbounds.length > 0 && <div className="text-right"><p className="text-xs text-muted-foreground">入口已记录累计</p><strong className="text-2xl numeric">{formatBytes(totalBytes({ upload_bytes: node.inbound_upload_bytes, download_bytes: node.inbound_download_bytes }))}</strong></div>}</div>
      <DataFreshness quality={node.quality} />
      {!inbounds.length ? <div role="status" className="notice">尚无入口统计快照，请在节点详情“更新绑定”启用统计，或检查采集服务。未知数据不按零展示。</div> : <>
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">{[...protocols].map(([protocol, bytes]) => <div key={protocol} className="rounded-lg border p-3"><strong className="text-sm">{protocol}</strong><p className="numeric mt-1">{formatBytes(totalBytes(bytes))}</p></div>)}</div>
        <Table><TableHeader><TableRow><TableHead>入口 / 协议</TableHead><TableHead>端口</TableHead><TableHead>上传</TableHead><TableHead>下载</TableHead><TableHead>统计起点</TableHead></TableRow></TableHeader><TableBody>{inbounds.map(row => <TableRow key={row.tag}><TableCell><strong>{row.tag}</strong><p className="text-xs text-muted-foreground">{row.protocol} · {row.active ? '当前入口' : '已移除，保留历史'}</p></TableCell><TableCell>{row.port || '—'}</TableCell><TableCell className="numeric">{formatBytes(row.upload_bytes)}</TableCell><TableCell className="numeric">{formatBytes(row.download_bytes)}</TableCell><TableCell className="text-xs">{formatTime(row.started_at)}</TableCell></TableRow>)}</TableBody></Table>
      </>}
      <p className="text-xs text-muted-foreground">入口累计包含可识别用户、匿名/共享密码用户和运维探针；与用户用量是两个统计角度，不应相加。首次开启从采集基线开始，不回填过去的流量。</p>
    </section>
  })}</div>
}
export function LocalUserTraffic({ nodes }: { nodes: Schema['Node'][] }) {
  const rows = nodes.flatMap(node => (node.local_users ?? []).map(user => ({ node, user })))
  if (!rows.length) return <p className="panel p-6 text-muted-foreground">尚无本地命名用户记录。匿名和共享密码连接仅计入入口；运维探针不计入用户用量。</p>
  return <section className="panel p-5 space-y-4"><h2>节点本地用户</h2><p className="text-sm text-muted-foreground">来自现有 sing-box 配置，只统计用量，不自动授予 sbctl 管理权限。同一节点的相同用户名合并统计。</p><Table><TableHeader><TableRow><TableHead>节点 / 用户</TableHead><TableHead>上传</TableHead><TableHead>下载</TableHead><TableHead>统计起点</TableHead><TableHead>数据质量</TableHead></TableRow></TableHeader><TableBody>{rows.map(({node,user}) => <TableRow key={`${node.id}:${user.name}`}><TableCell><strong>{user.name}</strong><p className="text-xs text-muted-foreground">{node.name} · {user.active ? '当前用户' : '保留历史'}</p></TableCell><TableCell className="numeric">{formatBytes(user.upload_bytes)}</TableCell><TableCell className="numeric">{formatBytes(user.download_bytes)}</TableCell><TableCell className="text-xs">{formatTime(user.started_at)}</TableCell><TableCell><DataFreshness quality={node.quality} compact /></TableCell></TableRow>)}</TableBody></Table></section>
}
