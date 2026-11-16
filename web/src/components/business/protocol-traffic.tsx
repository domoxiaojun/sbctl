import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import type { Schema, Quality } from '@/api/types'
import { DataFreshness } from './status'
import { DataState, Pagination } from './common'
import { exactBytes, formatBytes, formatTime, totalBytes } from '@/lib/format'
import { sortTraffic, trafficTotals } from '@/lib/traffic'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'

type Row = { id: string; name: string; description: string; port?: number; upload_bytes: string; download_bytes: string; started_at?: string; quality?: Quality; user_id?: string }
function TrafficRows({ rows }: { rows: Row[] }) {
 const [selectedPage, setPage] = useState(1)
 const page = Math.min(selectedPage, Math.max(1, Math.ceil(rows.length / 10)))
 const items = rows.slice((page - 1) * 10, page * 10)
 const name = (row: Row) => row.user_id ? <Link className="font-medium hover:text-primary" to="/users/$userId" params={{userId:row.user_id}} search={{}}>{row.name}</Link> : <strong className="break-all">{row.name}</strong>
 const bytes = (value: string) => <span title={exactBytes(value)} className="numeric">{formatBytes(value)}</span>
 return <><div className="desktop-table"><Table><TableHeader><TableRow><TableHead>统计对象</TableHead><TableHead>上传</TableHead><TableHead>下载</TableHead><TableHead>合计</TableHead><TableHead>首次纳入采集</TableHead></TableRow></TableHeader><TableBody>{items.map(row => <TableRow key={row.id}><TableCell>{name(row)}<p className="text-xs text-muted-foreground mt-1">{row.description}{row.port ? ` · :${row.port}` : ''}</p></TableCell><TableCell>{bytes(row.upload_bytes)}</TableCell><TableCell>{bytes(row.download_bytes)}</TableCell><TableCell>{bytes(totalBytes(row))}</TableCell><TableCell className="text-xs">{formatTime(row.started_at ?? row.quality?.started_at ?? null)}{row.quality && <DataFreshness quality={row.quality} compact/>}</TableCell></TableRow>)}</TableBody></Table></div>
 <div className="mobile-cards">{items.map(row => <article className="mobile-record" key={row.id}><div className="flex flex-wrap justify-between gap-2">{name(row)}<strong>{bytes(totalBytes(row))}</strong></div><p className="text-xs text-muted-foreground mt-2 break-all">{row.description}{row.port ? ` · :${row.port}` : ''}</p><div className="record-metrics"><span>上传 {bytes(row.upload_bytes)}</span><span>下载 {bytes(row.download_bytes)}</span></div>{row.quality && <DataFreshness quality={row.quality} compact/>}</article>)}</div>{rows.length > 10 && <Pagination data={{page,page_size:10,total:rows.length}} onChange={next => setPage(next.page ?? 1)} />}</>
}
export function ProtocolTraffic({ nodes, period = false, sort }: { nodes: Schema['Node'][]; period?: boolean; sort?: string }) {
 const visible = nodes.filter(node => node.inbounds.length)
 if (!visible.length) return <DataState title="没有可显示的入口记录"/>
 return <div className="space-y-5">{visible.map(node => {
  const inbounds = sortTraffic(node.inbounds, sort, row => row.tag)
  const protocols = [...new Set(inbounds.map(i => i.protocol))]
  return <section className="panel overflow-hidden" key={node.id}>
   <div className="p-5 space-y-3"><div className="flex flex-wrap justify-between gap-3"><div><h2><Link to="/nodes/$nodeId" params={{nodeId:node.id}} search={{}} className="hover:text-primary">{node.name}</Link></h2><p className="text-xs text-muted-foreground mt-2">匹配 {inbounds.length} 个入口 · 客户端→节点为上传，节点→客户端为下载</p></div><DataFreshness quality={node.quality}/></div>
    <div className="flex flex-wrap gap-2">{protocols.map(protocol => <span className="rounded-md border px-3 py-2 text-xs" key={protocol}>{protocol.toUpperCase()} <strong className="numeric ml-2">{formatBytes(totalBytes(trafficTotals(inbounds.filter(i => i.protocol === protocol))))}</strong></span>)}</div>
   </div>
   <TrafficRows rows={inbounds.map(row => ({...row,id:row.tag,name:row.tag,description:`${row.protocol.toUpperCase()} · ${row.active ? '当前入口' : '已移除，保留历史'}`}))}/>
   <p className="p-5 border-t text-xs text-muted-foreground">{period ? '区间' : '累计'}入口用量包含匿名、共享密码连接及运维探针。VMess 等现有入口在这里查看；与 sbctl 用户用量不相加。</p>
  </section>
 })}</div>
}
export function LocalUserTraffic({ nodes, period = false, sort }: { nodes: Schema['Node'][]; period?: boolean; sort?: string }) {
 const rows: Row[] = nodes.flatMap(node => node.local_users.map(user => ({...user,id:`${node.id}:${user.name}`,name:user.name,description:`${node.name} · ${user.active ? '当前用户' : '已移除，保留历史'}`,quality:node.quality})))
 if (!rows.length) return <DataState title="没有匹配的本地命名用户"/>
 return <section className="panel overflow-hidden"><div className="p-5"><h2>节点本地用户 · {period ? '区间用量' : '累计用量'}</h2><p className="text-sm text-muted-foreground mt-2">同一节点的相同用户名合并。匿名及共享密码连接只归属入口。</p></div><TrafficRows rows={sortTraffic(rows,sort,r=>r.name)}/></section>
}
export function ManagedUserTraffic({ users, period = false, sort }: { users: Schema['TrafficUser'][]; period?: boolean; sort?: string }) {
 if (!users.length) return <DataState title="没有匹配的 sbctl 用户用量"/>
 const rows: Row[] = users.map(u => ({...u,id:`${u.node_id}:${u.user_id}`,name:u.display_name,description:u.node_name}))
 return <section className="panel overflow-hidden"><div className="p-5"><h2>sbctl 用户 · {period ? '区间用量' : '累计用量'}</h2><p className="text-sm text-muted-foreground mt-2">按用户与节点分别列出，仅包括通过 sbctl 管理的身份。</p></div><TrafficRows rows={sortTraffic(rows,sort,r=>r.name)}/></section>
}
