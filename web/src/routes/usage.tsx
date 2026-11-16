import { PageHeader } from '@/components/business/common'
import { TrafficExplorer } from '@/components/business/traffic-explorer'
export default function UsagePage() {
 return <><PageHeader title="流量统计" description="按节点、协议和日期查看 sing-box 用量。入口与用户分别统计，两个口径不相加。"/><TrafficExplorer/></>
}
