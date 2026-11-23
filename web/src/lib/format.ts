export function totalBytes(row: { upload_bytes: string; download_bytes: string }) { return (BigInt(row.upload_bytes) + BigInt(row.download_bytes)).toString() }
export function byteParts(input: string): { value: string; unit: string } {
  const value = BigInt(input)
  if (value < 0n) throw new RangeError('Byte counts must be nonnegative')
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB', 'EiB']
  let index = 0, divisor = 1n
  while (index < units.length - 1 && value >= divisor * 1024n) { index++; divisor *= 1024n }
  const whole = value / divisor, fraction = (value % divisor) * 100n / divisor
  return { value: `${whole.toLocaleString('en-US')}${index > 0 ? `.${fraction.toString().padStart(2, '0')}` : ''}`, unit: units[index] }
}
export function formatBytes(input: string) { const p = byteParts(input); return `${p.value} ${p.unit}` }
export function exactBytes(input: string) { return `${BigInt(input).toLocaleString('en-US')} 字节` }
export function formatRate(bytesPerSecond: number) {
  if (!Number.isFinite(bytesPerSecond) || bytesPerSecond <= 0) return '0 B/s'
  const units = ['B/s', 'KiB/s', 'MiB/s', 'GiB/s', 'TiB/s']
  let index = 0, value = bytesPerSecond
  while (index < units.length - 1 && value >= 1024) { index++; value /= 1024 }
  return `${index === 0 ? Math.round(value) : value >= 100 ? value.toFixed(1) : value.toFixed(2)} ${units[index]}`
}
export function usageChartScale(items: { upload_bytes: string; download_bytes: string }[]) {
  const maximum = items.reduce((max, row) => { const value = BigInt(totalBytes(row)); return value > max ? value : max }, 0n)
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB', 'EiB']
  let index = 0, divisor = 1
  while (index < units.length - 1 && maximum >= BigInt(divisor) * 1024n) { index++; divisor *= 1024 }
  return { unit: units[index], value: (bytes: string) => Number(BigInt(bytes)) / divisor }
}
export function formatTime(value: string | null | undefined, full = false) {
  if (!value) return '尚无记录'
  return new Intl.DateTimeFormat('zh-CN', { timeZone: 'Asia/Singapore', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', ...(full ? { year: 'numeric', second: '2-digit' } : {}), hour12: false }).format(new Date(value))
}
export const operationNames = { create_user: '添加用户', disable_user: '禁用用户', enable_user: '重新启用', update_grants: '调整授权', rotate_credentials: '轮换凭证', publish: '发布配置' } as const
export const accountingModeNames = { both: '双向合计', download_only: '仅计下载', upload_only: '仅计上传', max_direction: '单向取大' } as const
