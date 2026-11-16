# 总览与统计改进验收

日期：2026-11-16，Asia/Singapore。版本：0.8.0。

## 交付范围

参考 Neko Master 的[时间选择](https://github.com/foru17/neko-master/blob/main/apps/web/components/common/time-range-picker.tsx)、[总览组织](https://github.com/foru17/neko-master/blob/main/apps/web/components/overview/index.tsx)和[时间聚合](https://github.com/foru17/neko-master/blob/main/apps/collector/src/database/repositories/timeseries.repository.ts)思路，按 sbctl 的现有组件与账本自行实现，没有复制其源文件或引入新依赖。

- 日期快捷范围和自定义原生日历放入弹层，高级状态/排序按需展开。
- 总览默认近 7 天，区间汇总、每日趋势、节点和协议/用户排行前置；保留节点健康、操作和告警。
- 日趋势按 UTC+8 返回已记录用量，未知日使用 null，部分节点缺少记录时整体标记 partial；不把缺失记录绘制成零。
- 趋势、汇总、排行与明细使用相同筛选。全部累计模式下图表明确标注最近 7 天。
- 点击图表柱形或每日数据中的日期定位当天；点击排行定位节点、协议或精确用户。同名受管用户按 ID 分开，单个用户跨节点累计聚合。
- 明细每页 10 条，筛选变更重置分页，轮询保持当前页；汇总和排行仍覆盖全部筛选结果。

本轮复用 0.7.0 日期账本，不改变采集协议，不新增分钟采样、域名/IP/规则分析，也不修改节点代理。

## 验证

- `make check`：Go vet 和全量竞争检测通过。覆盖每日/区间一致、跨日、重复采样、旧账本基线、大整数、精确同名用户、完整/部分/未知日期与混合节点覆盖。
- 实际 Go HTTP 响应通过 OpenAPI 校验；前端生成契约一致。
- 13 项前端单元测试通过，包含跨节点身份聚合和日趋势与区间汇总一致性；TypeScript 通过。
- ESLint 无错误，保留 3 条既有 UI 组件 Fast Refresh 提示；生产构建和演示内容排除检查通过。
- 桌面/手机覆盖日期弹层、非法草稿不提交、协议/节点/用户排行、图表和日期表联动、网址重载、总览到明细、分页与缺失数据。正式发行 CI 继续执行这些用例与用户生命周期流程。
- 已检查[桌面总览](overview-1440.png)和[手机总览](overview-375.png)；相关文档 21 处链接均通过检查，发行工作流 actionlint 通过。

## 发布和线上状态

本地实现与必要验证完成，等待发行及主控更新后补充真实回执。上线沿用现有 Linux CI 和已校验私有镜像交付流程；yxvmhk 继续使用兼容节点程序。
