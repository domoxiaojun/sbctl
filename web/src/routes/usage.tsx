import { PageHeader } from '@/components/business/common'
import { TrafficExplorer } from '@/components/business/traffic-explorer'
export default function UsagePage() {
 return <><PageHeader title="流量统计" description="按节点、协议和日期查看 sing-box 入口与节点本地用户用量。"/><TrafficExplorer/></>
}
