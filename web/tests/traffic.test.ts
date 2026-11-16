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

import { hourlyAllowed, trafficCsv } from '../src/lib/traffic'
it('按小时趋势限制在 7 天内并与按日合计一致', () => {
 expect(hourlyAllowed('', '')).toBe(true)
 expect(hourlyAllowed('2026-01-01', '2026-01-07')).toBe(true)
 expect(hourlyAllowed('2026-01-01', '2026-01-08')).toBe(false)
 expect(trafficSearch({ granularity: 'hour' }).granularity).toBe('hour')
 expect(trafficSearch({ granularity: 'week' }).granularity).toBeUndefined()
 const now = Date.parse('2026-03-10T05:30:00Z'), seed = fixtures(now), day = calendarDate(now)
 const params = { scope: 'inbounds', from: calendarDate(now - 86400000), to: day }
 const daily = demoTraffic(seed, new URLSearchParams(params), now), hourly = demoTraffic(seed, new URLSearchParams({ ...params, granularity: 'hour' }), now)
 expect(daily.granularity).toBe('day')
 expect(hourly.granularity).toBe('hour')
 // 昨天 24 小时，加上今天 00 时至当前 13 时（UTC+8）。
 expect(hourly.trend).toHaveLength(24 + 14)
 expect(hourly.trend.at(-1)!.date).toBe(`${day}T13:00`)
 const sum = (points: typeof daily.trend) => trafficTotals(points.map(p => ({ upload_bytes: p.upload_bytes ?? '0', download_bytes: p.download_bytes ?? '0' })))
 expect(sum(hourly.trend)).toEqual(sum(daily.trend))
 expect(() => demoTraffic(seed, new URLSearchParams({ from: '2026-03-01', to: '2026-03-10', granularity: 'hour' }), now)).toThrow()
})
it('导出 CSV 保留精确字节并防止公式注入', () => {
 const now = Date.now(), seed = fixtures(now), node = seed.nodes[0]
 for (const other of seed.nodes.slice(1)) { other.inbounds = []; other.local_users = [] }
 node.inbounds = [{ tag: '=cmd|"x",1', protocol: 'vmess', port: 443, active: false, started_at: '2026-01-01T00:00:00Z', upload_bytes: '9007199254740993', download_bytes: '7' }]
 node.local_users = [{ name: '张三', active: true, started_at: '2026-01-02T00:00:00Z', upload_bytes: '1', download_bytes: '2' }]
 const inbounds = trafficCsv(demoTraffic(seed, new URLSearchParams(), now), 'inbounds')
 expect(inbounds.startsWith('﻿节点,节点 ID,入口,协议,端口,记录状态,上传字节,下载字节,合计字节,首次纳入采集,统计范围\r\n')).toBe(true)
 const row = inbounds.split('\r\n')[1]
 expect(row).toContain(`"'=cmd|""x"",1",vmess,443,已移除,9007199254740993,7,9007199254741000,2026-01-01T00:00:00Z,全部累计`)
 const local = trafficCsv(demoTraffic(seed, new URLSearchParams({ scope: 'local' }), now), 'local')
 expect(local.split('\r\n')[1]).toBe(`${node.name},${node.id},张三,当前,1,2,3,2026-01-02T00:00:00Z,全部累计`)
 expect(local.endsWith('\r\n')).toBe(true)
})

import { monthRange } from '../src/lib/traffic'
it('按月趋势由每日数据折叠，点击月份定位到整月且不超过今天', () => {
 const now = Date.parse('2026-03-10T05:30:00Z'), seed = fixtures(now)
 expect(monthRange('2026-02', now)).toEqual({ from: '2026-02-01', to: '2026-02-28' })
 expect(monthRange('2024-02', now)).toEqual({ from: '2024-02-01', to: '2024-02-29' })
 expect(monthRange('2026-03', now)).toEqual({ from: '2026-03-01', to: '2026-03-10' })
 expect(trafficSearch({ granularity: 'month' }).granularity).toBe('month')
 const params = { scope: 'inbounds', from: '2026-02-20', to: '2026-03-10' }
 const daily = demoTraffic(seed, new URLSearchParams(params), now), monthly = demoTraffic(seed, new URLSearchParams({ ...params, granularity: 'month' }), now)
 expect(monthly.granularity).toBe('month')
 expect(monthly.trend.map(p => p.date)).toEqual(['2026-02', '2026-03'])
 const sum = (points: typeof daily.trend) => trafficTotals(points.map(p => ({ upload_bytes: p.upload_bytes ?? '0', download_bytes: p.download_bytes ?? '0' })))
 expect(sum(monthly.trend)).toEqual(sum(daily.trend))
 // 演示数据只有最近 14 天明细：二月混有缺失日期，记为不完整。
 expect(monthly.trend[0].status).toBe('partial')
 const recent = demoTraffic(seed, new URLSearchParams({ granularity: 'month' }), now)
 expect(recent.trend).toHaveLength(12)
 expect(recent.trend.at(-1)!.date).toBe('2026-03')
 expect(recent.trend[0]).toMatchObject({ date: '2025-04', status: 'unavailable', upload_bytes: null })
})
