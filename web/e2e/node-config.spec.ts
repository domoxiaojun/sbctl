import { test, expect, type Page } from '@playwright/test'
import { fixtures } from '../src/demo/fixtures'

const nodeId = fixtures(Date.now()).nodes[0].id

async function openSections(page: Page) {
 await page.goto(`/nodes/${nodeId}`)
 const panel = page.getByRole('region', { name: '配置片段' })
 await expect(panel).toBeVisible()
 await panel.getByRole('button', { name: '读取配置', exact: true }).click()
 await expect(panel.getByRole('textbox', { name: '路由配置 JSON' })).toBeVisible()
 return panel
}

test('配置片段：读取、校验、保存并重启、还原', async ({ page }) => {
 const errors: string[] = []
 page.on('pageerror', error => errors.push(error.message))
 const panel = await openSections(page)
 const editor = panel.getByRole('textbox', { name: '路由配置 JSON' }), save = panel.getByRole('button', { name: '保存并重启', exact: true })
 await expect(editor).toHaveValue(/"final": "direct"/)
 await expect(save).toBeDisabled()
 await editor.fill('{ "final": ')
 await expect(panel.getByRole('alert')).toContainText('JSON 格式无效')
 await expect(save).toBeDisabled()
 await editor.fill('[]')
 await expect(panel.getByRole('alert')).toContainText('需要一个 JSON 对象')
 await panel.getByRole('button', { name: '还原', exact: true }).click()
 await expect(editor).toHaveValue(/"final": "direct"/)
 await editor.fill('{"final":"block","rules":[]}')
 await panel.getByRole('button', { name: '格式化', exact: true }).click()
 await expect(editor).toHaveValue('{\n  "final": "block",\n  "rules": []\n}')
 await expect(panel.getByRole('button', { name: /^路由/ })).toContainText('•')
 await save.click()
 const dialog = page.getByRole('dialog')
 await expect(dialog).toContainText('现有连接短暂中断')
 await dialog.getByRole('button', { name: '取消', exact: true }).click()
 await expect(panel.getByRole('status')).toHaveCount(0)
 await save.click()
 await page.getByRole('dialog').getByRole('button', { name: '确认保存并重启', exact: true }).click()
 await expect(panel.getByRole('status')).toContainText('路由配置已保存')
 await expect(page.getByRole('dialog')).toHaveCount(0)
 await expect(save).toBeDisabled()
 await page.reload()
 const reloaded = page.getByRole('region', { name: '配置片段' })
 await reloaded.getByRole('button', { name: '读取配置', exact: true }).click()
 await expect(reloaded.getByRole('textbox', { name: '路由配置 JSON' })).toHaveValue(/"final": "block"/)
 await expect(page.getByRole('region', { name: '节点管理任务' })).toContainText('变更配置片段')
 await reloaded.getByRole('button', { name: 'DNS', exact: true }).click()
 await expect(reloaded.getByRole('textbox', { name: 'DNS配置 JSON' })).toHaveValue(/"servers"/)
 expect(errors).toEqual([])
})

test('配置片段：预检失败时保留编辑内容并提示原因', async ({ page }) => {
 const panel = await openSections(page)
 const editor = panel.getByRole('textbox', { name: '路由配置 JSON' })
 await editor.fill('{"final":"demo-preflight-failure"}')
 await panel.getByRole('button', { name: '保存并重启', exact: true }).click()
 const dialog = page.getByRole('dialog')
 await dialog.getByRole('button', { name: '确认保存并重启', exact: true }).click()
 await expect(dialog.getByRole('alert')).toContainText('候选配置预检失败')
 await dialog.getByRole('button', { name: '取消', exact: true }).click()
 await expect(editor).toHaveValue('{"final":"demo-preflight-failure"}')
 await expect(panel.getByRole('status')).toHaveCount(0)
})

test('配置片段在手机宽度下不溢出', async ({ page }) => {
 await page.setViewportSize({ width: 375, height: 900 })
 const panel = await openSections(page)
 await expect(panel.getByRole('group', { name: '配置片段' }).getByRole('button')).toHaveCount(4)
 expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
})
