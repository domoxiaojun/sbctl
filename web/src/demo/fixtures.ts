import type { Schema, Quality } from '../api/types'
export const DEMO_FIXTURE_MARKER = 'SBCTL_DEMO_FIXTURE_ONLY'
export function fixtures(now: number) {
  const time = (seconds = 0) => new Date(now - seconds * 1000).toISOString()
  const quality = (stale = false, gap = false): Quality => ({ freshness: stale ? 'stale' : 'current', integrity_state: 'ok', has_historical_gap: gap, gap_count: gap ? 1 : 0, started_at: time(86400 * 14), sampled_at: time(stale ? 3600 : 12), synced_at: time(30) })
  const regions = [{ id: 'sg', name: '新加坡', count: 6 }, { id: 'jp', name: '日本', count: 5 }, { id: 'hk', name: '中国香港', count: 4 }, { id: 'us', name: '美国', count: 3 }, { id: 'de', name: '德国', count: 2 }]
  let index = 0
  const nodes: Schema['Node'][] = regions.flatMap(region => Array.from({ length: region.count }, (_, i) => {
    index++
    const id = `${region.id}-${String(i + 1).padStart(2, '0')}`
    const status = ['us-03', 'hk-04'].includes(id) ? 'offline' : id === 'jp-05' ? 'degraded' : 'online'
    const agent_state = status === 'offline' ? 'offline' : index % 5 === 0 ? 'unregistered' : 'online'
    const online = status === 'online'
    const upBps = online ? (index * 42_000) : 0
    const downBps = online ? (index * 310_000) : 0
    const activeConns = online ? (3 + (index % 9)) : 0
    const quotaBytes = (1099511627776n).toString() // 1 TiB
    const usedBytes = (BigInt(index * 45) * 1073741824n).toString()
    const usedPercent = Math.round(Number(BigInt(usedBytes) * 10000n / BigInt(quotaBytes))) / 100
    const billingState: Schema['NodeBillingView']['state'] = usedPercent >= 100 ? 'exceeded' : usedPercent >= 80 ? 'warning' : 'normal'
    return {
      id,
      name: `${region.name} ${String(i + 1).padStart(2, '0')}`,
      region: region.id,
      location: region.name,
      management_address: `100.64.10.${index}`,
      public_host: `${id}.example.invalid`,
      protocols: ['AnyTLS', 'TUIC v5'],
      status,
      version: '1.14.4-sbctl.2',
      revision: 24 + i,
      users_count: 0,
      local_users: [],
      total_inbounds: 2,
      inbound_upload_bytes: '0',
      inbound_download_bytes: '0',
      outbounds: [
        { tag: 'direct', active: true, started_at: new Date(now).toISOString(), upload_bytes: '0', download_bytes: '0' },
        { tag: 'warp-out', active: true, started_at: new Date(now).toISOString(), upload_bytes: '0', download_bytes: '0' },
      ],
      outbound_upload_bytes: '0',
      outbound_download_bytes: '0',
      upload_bps: upBps,
      download_bps: downBps,
      active_connections: activeConns,
      billing: {
        quota_bytes: quotaBytes,
        used_bytes: usedBytes,
        upload_bytes: (BigInt(usedBytes) / 4n).toString(),
        download_bytes: (BigInt(usedBytes) * 3n / 4n).toString(),
        used_percent: usedPercent,
        reset_day: 1,
        accounting_mode: 'both',
        warn_percent: 80,
        cycle_from: '2026-11-01',
        cycle_to: '2026-11-30',
        state: billingState,
        completeness: 'complete',
      },
      config_event_seq: 1,
      config_semantic_hash: '0'.repeat(64),
      config_sync_state: 'synced',
      config_sync_error: null,
      config_external_drift: false,
      config_sync_at: new Date(now).toISOString(),
      agent_state,
      agent_version: agent_state === 'unregistered' ? '' : '1.14.4-sbctl.2',
      agent_last_seen: agent_state === 'unregistered' ? null : time(status === 'offline' ? 600 : 5),
      inbounds: [
        { tag: 'anytls-in', protocol: 'anytls', port: 5443, active: true, started_at: new Date(now).toISOString(), upload_bytes: '0', download_bytes: '0' },
        { tag: 'tuic-in', protocol: 'tuic', port: 4443, active: true, started_at: new Date(now).toISOString(), upload_bytes: '0', download_bytes: '0' },
      ],
      quality: quality(status !== 'online', id === 'jp-05'),
    }
  }))
  const groups: Schema['Group'][] = [{ id: 'apac', name: '亚太优选', node_ids: nodes.filter(n => ['sg', 'jp', 'hk'].includes(n.region)).map(n => n.id) }, { id: 'global', name: '全部节点', node_ids: nodes.map(n => n.id) }, { id: 'sg', name: '新加坡', node_ids: nodes.filter(n => n.region === 'sg').map(n => n.id) }]
  const names = ['阿川', '小林', 'Mia', '陈同学', 'Alex', '一木', '小舟', '何先生', 'Nora', '晚风', '周同学', '远山']
  const users: Schema['User'][] = names.map((display_name, i) => {
    const group = groups[i % groups.length]
    const targetNodes = nodes.filter(n => group.node_ids.includes(n.id))
    targetNodes.forEach(node => {
      node.users_count += i === 11 ? 0 : 1
    })
    return { metadata_version: 0, archived: false, archived_at: null, subscription_generation: 1, id: `usr-${i + 1}`, display_name, note: ['个人使用', '团队成员', '测试设备'][i % 3], status: i === 11 ? 'disabled' : 'active', revision: 1, group_ids: [group.id], node_count: i === 11 ? 0 : targetNodes.length, subscription_state: i === 11 ? 'revoked' : 'published', created_at: time(86400 * (20 - i)), quality: quality(i === 7, i === 3) }
  })
  const operations: Schema['Operation'][] = [{ id: 'op-demo-001', kind: 'disable_user', subject: '远山', user_id: 'usr-12', state: 'partial', created_at: time(300), targets: nodes.slice(0, 5).map((n, i) => ({ node_id: i === 4 ? 'hk-04' : n.id, name: i === 4 ? '中国香港 04' : n.name, state: i === 4 ? 'unreachable' : 'verified', message: i === 4 ? '等待管理链路恢复' : '旧凭证已撤销' })), subscription_state: 'revoked' }, { id: 'op-demo-002', kind: 'rotate_credentials', subject: 'Mia', user_id: 'usr-3', state: 'completed', created_at: time(2200), targets: nodes.slice(0, 6).map(n => ({ node_id: n.id, name: n.name, state: 'verified', message: '新凭证已验证' })), subscription_state: 'published' }, { id: 'op-demo-003', kind: 'create_user', subject: '周同学', user_id: 'usr-11', state: 'completed', created_at: time(3600), targets: nodes.slice(0, 6).map(n => ({ node_id: n.id, name: n.name, state: 'verified', message: '配置已应用并验证' })), subscription_state: 'published' }]
  const alerts: Schema['Alert'][] = [{ id: 'al-1', title: '远山仍有节点待撤销', description: '中国香港 04 的管理链路不可达，恢复后将优先执行撤销。', severity: 'high', state: 'open', node_id: 'hk-04', created_at: time(300) }, { id: 'al-2', title: '美国 03 管理链路失联', description: '保留最后已记录累计，当前节点状态需要确认。', severity: 'medium', state: 'open', node_id: 'us-03', created_at: time(1800) }, { id: 'al-3', title: '日本 05 存在历史统计缺口', description: '一次重启前的尾部流量无法量化，恢复采样不会清除此标记。', severity: 'info', state: 'open', node_id: 'jp-05', created_at: time(7200) }]
  return { marker: DEMO_FIXTURE_MARKER, nodes, users, groups, operations, alerts }
}
