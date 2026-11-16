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

0.8.0 已发布并在 ovhus 上线。源码提交 `ace76f5ca12ac32b8e991258002c3aae953aaed1`，发行 CI `43816287268` 的双架构及浏览器流程全部通过；私有镜像导出 CI `43816973319` 成功。

- 发行镜像：`ghcr.io/domoxiaojun/sbctl@sha256:e960f4dbc12d7a38d40d8e28dda9b48ac2f0d5363eafcfc2ba022c13c590991a`。
- amd64 配置摘要：`sha256:27936dc183c7f5adcc60993c5205e6c7939216423cf8c46844c1ea38dadfba50`，导入后重新导出核验相同；宿主程序与镜像内程序摘要一致。
- 主控备份：`/opt/sbctl/backups/release-v0.8.0-control-20261116T063538Z`。四个服务正常，管理和订阅健康接口均报告 0.8.0。
- 本地下载产物较慢，已中止该只读下载，改由 ovhus 使用短时授权地址取得同一 CI 产物；没有向服务器保存 GitHub 登录凭据。
- 密码、主密钥和配置摘要相同；全部旧入口、本地用户与受管用户累计不下降。0.7.0 日期基线保持为 2026-11-16 13:55:46（UTC+8）。
- yxvmhk 的两个 sbctl 服务 active，保留兼容的 0.6.1 程序；代理容器 ID 和启动时间均未变。

14:36 的 HTTPS 验证：三种统计范围的日用量相加均等于同次响应中的区间汇总；近 7 天的 6 个缺失日为 null，当天为 partial。VMess 日期用量为上传 218,190、下载 578,327 字节，后续页面刷新继续增长。原 12 处历史缺口保留，采样 current/ok。

真实桌面/手机测试通过协议和节点排行、柱形日期选择、总览到明细、旧日期未知提示、日期重选及刷新；浏览器无脚本错误。测试后注销会话。已查看[线上桌面总览](live-overview-1440.png)和[线上手机明细](live-usage-375.png)。[机器回执](live-acceptance.json)和[发行回执](release.json)一并保留。本轮达到停止条件。
