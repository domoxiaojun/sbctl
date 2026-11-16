import { test, expect } from '@playwright/test'
import { fixtures } from '../src/demo/fixtures'
import { calendarDate } from '../src/lib/traffic'

for (const width of [375,1440]) test(`流量筛选、日期、刷新和节点总览 ${width}px`,async({page},testInfo)=>{
 await page.setViewportSize({width,height:1000})
 await page.emulateMedia({colorScheme:width===375?'dark':'light'})
 const seed=fixtures(Date.now())
 const n=seed.nodes[0]
 n.inbounds.push({tag:'vmess',protocol:'vmess',port:23323,active:true,started_at:new Date().toISOString(),upload_bytes:'397745587',download_bytes:'1203972810'})
 n.inbound_upload_bytes='397745587';n.inbound_download_bytes='1203972810'
 n.local_users.push({name:'vmess-user',active:true,started_at:new Date().toISOString(),upload_bytes:'397745587',download_bytes:'1203972810'})
 await page.addInitScript(seed=>sessionStorage.setItem('sbctl-f0-synthetic-state-v1',JSON.stringify({...seed,requests:{},signedIn:true})),seed)
 await page.goto(`/nodes/${n.id}`)
 await expect(page.getByRole('heading',{name:'全部协议入口流量'})).toBeVisible()
 await expect(page.locator('.detail-grid')).toContainText('1.49 GiB')
 await page.getByRole('combobox',{name:'筛选协议'}).click()
 await page.getByRole('option',{name:'VMESS',exact:true}).click()
 await expect(page).toHaveURL(/protocol=vmess/)
 await expect(page.getByLabel('筛选结果汇总')).toContainText('1 条记录')
 await page.getByRole('textbox',{name:'搜索流量记录'}).fill('23323')
 await expect(page.getByLabel('筛选结果汇总')).toContainText('1.49 GiB')
 await page.reload()
 await expect(page.getByRole('textbox',{name:'搜索流量记录'})).toHaveValue('23323')
 await page.getByRole('button',{name:'今天',exact:true}).click()
 await expect(page.getByLabel('开始日期')).not.toHaveValue('')
 await expect(page.getByLabel('筛选结果汇总')).toContainText('152.75 MiB')
 await expect(page.getByText('该时段记录不完整',{exact:false})).toBeVisible()
 await page.getByRole('button',{name:'刷新统计',exact:true}).click()
 await expect(page.getByRole('button',{name:'刷新统计',exact:true})).toBeEnabled()
 await expect(page.getByRole('status').filter({hasText:/统计已更新|已读取最新快照/})).toBeVisible()
 await page.getByLabel('结束日期').fill('2020-01-01')
 await expect(page.getByRole('alert')).toContainText('结束日期不能早于开始日期')
 await expect(page.getByRole('button',{name:'刷新统计',exact:true})).toBeDisabled()
 await page.getByRole('button',{name:'全部累计',exact:true}).click()
 await page.getByRole('button',{name:'节点本地用户',exact:true}).click()
 await expect(page.getByText('vmess-user',{exact:true}).filter({visible:true})).toBeVisible()
 await page.getByRole('button',{name:'协议入口',exact:true}).click()
 await page.getByRole('combobox',{name:'筛选协议'}).click()
 await page.getByRole('option',{name:'VMESS',exact:true}).click()
 await expect(page.getByLabel('筛选结果汇总')).toContainText('1.49 GiB')
 await expect(page.getByRole('button',{name:'刷新统计',exact:true})).toBeEnabled()
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
 await page.screenshot({path:testInfo.outputPath(`traffic-${width}.png`),fullPage:true})
 await page.goto('/usage')
 await page.getByRole('combobox',{name:'筛选流量节点'}).click()
 await page.getByRole('option',{name:n.name,exact:true}).click()
 await expect(page).toHaveURL(new RegExp(`node=${n.id}`))
 await page.getByRole('textbox',{name:'搜索流量记录'}).fill('no-such-entry')
 await expect(page.getByText('没有可显示的入口记录')).toBeVisible()
 await page.getByRole('button',{name:'重置筛选',exact:true}).click()
 await expect(page.getByRole('textbox',{name:'搜索流量记录'})).toHaveValue('')
})

test('生产页面自动更新快照，缺少历史明细时不显示零流量', async ({ page }) => {
 const node = fixtures(Date.now()).nodes[0]
 node.inbounds = [{ tag: 'vmess-live', protocol: 'vmess', port: 23323, active: true, started_at: new Date().toISOString(), upload_bytes: '1024', download_bytes: '4096' }]
 node.inbound_upload_bytes = '1024'; node.inbound_download_bytes = '4096'
 let downloaded = '4096'
 await page.route('**/api/v1/**', async route => {
  const url = new URL(route.request().url())
  if (url.pathname.endsWith('/session')) return route.fulfill({ json: { admin_id: 'test', display_name: '测试', csrf_token: 'test' } })
  if (url.pathname.endsWith('/traffic')) {
   const period = url.searchParams.has('from')
   const current = { ...node, inbound_download_bytes: downloaded, inbounds: [{ ...node.inbounds[0], download_bytes: downloaded }] }
   return route.fulfill({ json: { nodes: [current], users: [], periods: [{ node_id: node.id, history_started_at: null, status: period ? 'unavailable' : 'cumulative' }], from: url.searchParams.get('from') ?? '', to: url.searchParams.get('to') ?? '', timezone: 'Asia/Singapore', as_of: new Date().toISOString() } })
  }
  return route.fulfill({ status: 404, json: { code: 'NOT_FOUND', message: 'test' } })
 })
 await page.goto('http://127.0.0.1:14186/usage')
 await expect(page.getByLabel('筛选结果汇总')).toContainText('5.00 KiB')
 downloaded = '8192'
 await expect(page.getByLabel('筛选结果汇总')).toContainText('9.00 KiB', { timeout: 20_000 })
 await page.getByRole('button', { name: '今天', exact: true }).click()
 await expect(page.getByLabel('开始日期')).toHaveValue(calendarDate())
 await expect(page.getByText('该时段无日期明细', { exact: false })).toBeVisible()
 await expect(page.getByLabel('筛选结果汇总')).toContainText('—')
 await expect(page.getByLabel('筛选结果汇总')).not.toContainText('0 B')
})
