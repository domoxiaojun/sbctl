import { describe, expect, it } from 'vitest'
import { calendarDate, dateError, presetDates, sortTraffic, trafficSearch, trafficTotals } from '../src/lib/traffic'
describe('流量查询', () => {
 it('按 UTC+8 提供包含结束日的快捷日期', () => {
  const now=Date.parse('2026-01-31T16:15:00Z')
  expect(calendarDate(now)).toBe('2026-02-01')
  expect(presetDates('yesterday',now)).toEqual({from:'2026-01-31',to:'2026-01-31'})
  expect(presetDates('week',now)).toEqual({from:'2026-01-26',to:'2026-02-01'})
  expect(presetDates('month',now)).toEqual({from:'2026-02-01',to:'2026-02-01'})
 })
 it('拒绝无效及颠倒日期',()=>{
  expect(dateError('2026-01-01','')).toBeTruthy()
  expect(dateError('2026-02-30','2026-03-01')).toBeTruthy()
  expect(dateError('2026-02-02','2026-02-01')).toBeTruthy()
  expect(dateError('2026-01-01','2026-01-01')).toBe('')
 })
 it('筛选结果汇总和排序保留大整数精度',()=>{
  const rows=[{name:'b',upload_bytes:'9007199254740993',download_bytes:'1'},{name:'a',upload_bytes:'9007199254740994',download_bytes:'1'}]
  expect(sortTraffic(rows,'bytes',r=>r.name)[0].name).toBe('a')
  expect(trafficTotals(rows).upload_bytes).toBe('18014398509481987')
 })
})

import { trafficRankings } from '../src/lib/traffic'
import { demoTraffic } from '../src/demo/traffic'
import { fixtures } from '../src/demo/fixtures'
it('本地用户排行保持节点身份分离和大整数精度', () => {
 const seed = fixtures(Date.now())
 for (const node of seed.nodes) node.local_users = []
 for (const node of seed.nodes.slice(0, 2)) node.local_users = [{ name: '同名', upload_bytes: '9007199254740993', download_bytes: '1', started_at: new Date().toISOString(), active: true }]
 const report = demoTraffic(seed, new URLSearchParams('scope=local'), Date.now())
 const ranking = trafficRankings(report, 'local')
 expect(ranking.identities).toHaveLength(2)
 const first = ranking.identities.find(u => u.filter.node === seed.nodes[0].id)!
 expect(first.upload_bytes).toBe('9007199254740993')
 expect(first.filter.local_user).toBe('同名')
 expect(trafficTotals(ranking.nodes)).toEqual(trafficTotals(ranking.identities))
 const filtered = demoTraffic(seed, new URLSearchParams({scope: 'local', node: first.filter.node!, local_user: '同名'}), Date.now())
 expect(filtered.nodes).toHaveLength(1)
 expect(filtered.nodes[0].local_users[0].name).toBe('同名')
})
it('日趋势与所选时段汇总保持同一统计范围', () => {
 const seed = fixtures(Date.now()), from=calendarDate(Date.now()-6*86400000), to=calendarDate()
 seed.nodes[0].local_users=[{name:'local-history',active:true,started_at:new Date().toISOString(),upload_bytes:'102400',download_bytes:'51200'}]
 const report = demoTraffic(seed, new URLSearchParams({scope:'local',from,to}), Date.now())
 expect(report.trend).toHaveLength(7)
 expect(trafficTotals(report.trend.map(p => ({upload_bytes:p.upload_bytes!,download_bytes:p.download_bytes!})))).toEqual(trafficTotals(report.nodes.flatMap(n => n.local_users)))
})
it('旧用户分类网址不再启用该分类或用户筛选', () => {
 const search = trafficSearch({scope:'managed',user:'usr_old',node:'node_hk',from:'2026-01-01',to:'2026-01-02'})
 expect(search.scope).toBeUndefined()
 expect(search).not.toHaveProperty('user')
 expect(search.node).toBe('node_hk')
 expect(search.from).toBe('2026-01-01')
})
