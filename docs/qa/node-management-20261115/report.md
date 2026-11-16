# 节点管理流程验证报告

日期：2026-11-15（Asia/Singapore）

## 结果

- 主控发布为 Go `0.4.0`，ovhus 的 api、worker、sync、subscriptions 四个容器均运行 `sbctl-control:0.4.0`。
- yxvmhk 的 `sbctl-node` 与 `sbctl-collector` 均为 Go `0.4.0` 且 active。
- Tailscale 发现快照可用，列出 5 台主机；仅已登记的 yxvmhk 标记为已登记，未自动接管其它主机。
- yxvmhk 通过真实只读检查、确认接管和诊断：容器名 `sing-box`、镜像标签 `ghcr.io/domoxiaojun/sing-box:v1.12.9-v2ray-api-2`、镜像 digest `d9edd643…f033ce94`、配置目录 `/etc/sing-box`、AnyTLS/TUIC 两个入口、运行守卫和独立统计通道均通过。
- 接管过程保留用户修改的容器名、镜像及其它代理入口，只将安全挂载从旧路径对齐到 `/opt/sbctl/data/safety`。真实探针通过，节点 revision 保持 2，账本 ID 保持 `ledger_1b56f2ae913d7099ccc1ac318c2d5124`，采样序号由 530 增长到 582。
- 后续真实管理用户验收产生了非零累计：上传 1,708 字节、下载 7,404 字节；节点账本、主控同步和 `/usage` API 均返回该累计，之后用户按验收流程禁用但历史仍保留。

## 本地验证

- `go test -race ./...`、`go vet ./...`：通过。
- 前端 TypeScript、Vitest、OpenAPI 契约检查：通过。
- Playwright：18 项通过，包含 375px、1440px、暗色模式、发现主机、只读检查、确认接管和刷新恢复。
- ShellCheck、Hadolint：通过。

## 约束

首次接入仍需在目标主机安装固定 helper、配置受限 SSH 公钥、安装 checksum 验证的 grpcurl 和 `stats.proto`，并核对主机公钥。发现在线只表示 Tailscale 可见；网页不会自动以 root 执行任意 SSH/Docker 命令，也不会自动接管其它节点。

流量统计当前口径是 **sbctl 管理用户的 AnyTLS/TUIC 累计**。未纳入的范围包括其它 17 个 sing-box 入口、运维探针和 VPS 网卡总流量；页面已明确标注“受管用户累计”，避免把它误认为整台服务器账单。

## 后续统计问题修正

原验收只要求快照 freshness 为 current，可能在测试用户采样前就禁用，因此曾留下全零验收记录。现已要求测试用户上下行均出现非零累计后才禁用。另修复图表先按 MiB 做整数除法导致小流量被截为零的问题，改为自动选择单位并保留绘图小数；原始字节和提示仍保持整数精度。

修复已部署到主控，真实验收回执见 [流量验收](../../../ops/controller/traffic-acceptance-20261115.json)。本轮 Go 测试/vet、6 项 Vitest、类型/契约/生产构建以及 19 项浏览器回归通过。非零流量验证不等于已完成流量误差基准、全部协议统计或 VPS 总流量功能。
