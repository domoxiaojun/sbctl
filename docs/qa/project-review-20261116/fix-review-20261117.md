# 修复代码复审（2026-11-17）

基线：`456d404`。范围包括 R1–R4 修复 `3c7045d`，以及随后维护恢复、登录限流和多节点并发提交。开始时工作区干净；本轮只增加复审记录和更新计划。

确认 3 项 P2 问题，未发现新的 P1。原 R1–R4 及后续修复的现有定向回归通过；这些通过结果不覆盖下面的交错场景。

## F1 · P2 · 并发节点接管可将统计清单回写为旧版本

- 位置：[并行启动节点任务](../../../internal/sbctl/worker.go#L213)，[清单导出](../../../internal/sbctl/worker.go#L324)，[接管完成后导出](../../../internal/sbctl/node_management.go#L344)。
- `ExportInventory` 先读取控制库，提交读事务后再写共享 `nodes.json`，读取与替换之间没有锁或版本检查。并行接管不同节点时，A 可以读到较旧节点集合，B 提交新节点并导出新集合，最后 A 的旧集合覆盖 B。
- 结果：控制库和接管任务都显示成功，同步进程却只按被覆盖后的清单采集，遗漏节点持续缺少流量更新，直至下一次清单导出或 worker 重启。当前 worker 没有定期重建清单。
- 验证：在仓库外的当前提交副本中，仅在导出读事务结束后添加调度屏障；暂停旧导出，注册并导出第二节点，再恢复旧导出。清单从 2 个节点退回 1 个。屏障没有修改读取内容或原写入逻辑，此为受控交错复现。
- 建议：对读取快照到原子替换整个过程加共享锁，或交给单一发布者并校验版本；覆盖并行接入后的清单完整性。

## F2 · P2 · 维护领取没有避让同节点在途授权，导致正常任务误失败

- 位置：[节点任务领取](../../../internal/sbctl/node_management.go#L235)，[维护前置判断](../../../internal/sbctl/node_actions.go#L42)。
- `ClaimNodeJob` 只排除同节点的其他节点任务，没有排除 outbox 的有效租约。`Worker.Run` 可先启动撤销，再同时领取同节点已排队的重启或协议变更。
- 若维护从未在节点执行，回执查询得到 `missing`，任务立即以 `NODE_BUSY` 进入永久 `failed`，撤销完成后也不会自动恢复。用户需手动重试；这种正常排队不应变成失败。
- 验证：先创建并完成用户授权，再提交重启，随后禁用用户；模拟传输暂停撤销响应，运行并行 worker。重启在撤销仍在途时变为 `failed/NODE_BUSY`。此复现没有修改产品逻辑。
- 建议：领取新维护任务时也检查同节点授权租约；对等待授权完成的维护保留排队状态。同时保留原维护已完成或被中断时的恢复入口。

## F3 · P2 · deepmerge 补丁没有保护第一个参数的危险键复制

- 位置：[依赖补丁](../../../web/patches/deepmerge@4.3.1.patch#L8)。
- 补丁只扩展 `propertyIsUnsafe`，该函数用于复制第二个参数 `source`；`mergeObject` 复制第一个参数 `target` 的循环仍直接执行 `destination[key] = ...`。
- 在实际 shadcn 引用的补丁包上执行 `merge(JSON.parse('{"__proto__":{"review_polluted":true}}'), {})`，结果的 `review_polluted` 为继承的 `true`，且原型不再是 `Object.prototype`。全局 `Object.prototype` 未被改写；问题是返回对象的原型被污染。原来的 source 参数样例仍被正确拦截。
- 该结果证明本地补丁未完全实现其保护目标；未证明生产前端存在可利用路径，依赖仍限定在开发工具链。
- 建议：两个复制循环都安全定义属性或一致过滤危险键，补充 target、source 及嵌套输入的回归；不要只因 source 样例通过就判定公告已完全缓解。

## 验证与范围

- 当前仓库的统计端口变更、请求替代、完成/中断维护恢复、登录限流、受信任反代、并行 worker、多节点故障定向测试通过。
- 两项并发缺陷使用当前提交的隔离副本复现，竞态检测通过；测试断言的是缺陷行为，PASS 表示复现成功。复现测试保存在 [fix-review-repro_test.go.txt](fix-review-repro_test.go.txt)。F1 需要在隔离副本 `ExportInventory` 的数据库读事务结束后、`atomicWrite` 前添加 `if reviewInventoryReadHook != nil { reviewInventoryReadHook(nodes) }`，并声明 `var reviewInventoryReadHook func([]M)`；该屏障只控制合法执行顺序。
- `deepmerge` 复现直接使用 shadcn 当前解析到的补丁包。未重复全仓审查或运行生产部署、节点升级、真实容器/数据库服务。

本轮只报告问题，没有修改产品实现。

收尾时工作区另有 `schema/traffic.sql` 和 `traffic_history.go` 的并行修改，未包含在上述提交基线和本轮复审范围内。
