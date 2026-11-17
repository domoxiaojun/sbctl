# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目定位

sbctl 是私有的 sing-box 集群控制端：集中管理多台 VPS 上的 sing-box 节点（接管、协议入口维护、受控重启）、用户授权与 Surge/Mihomo 订阅、以及多角度累计/按日流量统计。后端是 Go（两个二进制共用一个包），控制台是 React。当前生产范围是 **ovhus 主控 + yxvmhk 单节点**，多节点推广尚未验收。

## 常用命令

Go（仓库根目录，Go 1.27.3）：

```sh
make test                 # go test ./...
make check                # go vet + go test -race，CI 的必过项
make build-linux          # dist/linux-{amd64,arm64}/{sbctl,sbctl-node}，CGO_ENABLED=0 静态程序
go test ./internal/sbctl -run '^TestName$' -count=1 -v    # 单个测试
go run ./cmd/sbctl help   # 主控子命令；节点侧为 go run ./cmd/sbctl-node help
go run ./cmd/sbctl export-openapi /tmp/openapi.json
```

前端（`web/`，Node 26.12.1 见 `.node-version`，pnpm 12.18.0 见 `packageManager`）。PATH 上没有 pnpm 或版本不符时，用 `npm exec --yes --package=pnpm@12.18.0 -- pnpm <命令>`：

```sh
pnpm install --frozen-lockfile
pnpm dev            # http://127.0.0.1:5186，演示数据，不需要后端
pnpm api:check      # 生成类型与 contracts/openapi.json 是否漂移
pnpm api:generate   # 契约变更后重新生成 src/api/generated/schema.d.ts
pnpm test           # vitest，只收 tests/**/*.test.ts（node 环境）
pnpm lint && pnpm typecheck
pnpm build          # typecheck + vite build + 演示内容排除检查，输出 web/dist
pnpm e2e            # Playwright；先 build，并先 pnpm exec playwright install chromium
pnpm exec vitest run tests/traffic.test.ts -t '用例名'              # 单个单元测试
pnpm exec playwright test e2e/traffic.spec.ts --grep '流量筛选'      # 单个浏览器场景
```

e2e 自行启动 `15186`（开发版）和 `14186`（生产预览，读 `web/dist`）两个服务，并拒绝复用已有服务。完整套件里的“视觉”用例会把截图写进 `docs/qa/management-20261116/overview-*.png`（已提交的历史验收材料）；不是在做新的验收截图时，跑完用 `git restore docs/qa/management-20261116/` 还原。

测试全部使用临时目录里的内嵌 SQLite 和 fake（`fakeTransport`、`fakeEngine`），**不需要也不应在本机运行 Docker、安装数据库或连接 VPS**；镜像只在 CI/Linux 服务器构建。

## 架构

### 两个二进制，一个包

`cmd/sbctl`（主控）和 `cmd/sbctl-node`（节点）只是 `sbctl.ControllerMain` / `sbctl.NodeMain` 的外壳，全部逻辑在单一包 `internal/sbctl`，子命令分发在 `cli.go`。

主控以同一镜像的四个 Compose 服务运行（`ops/controller/compose.yaml`），进程之间**只通过 SQLite 和文件交接**，各自挂载最小权限。worker（`Worker.Run`）和 sync（`SyncNodes`）对不同节点并行，同一节点的互斥由领取任务的 SQL 保证：

| 服务 | 子命令 | 读写 |
| --- | --- | --- |
| api | `serve` | control.db、主密钥、公开订阅目录（吊销）；只读 telemetry 导出与 Tailscale 发现快照；同时提供 `web/dist` |
| worker | `worker` | control.db、主密钥、发布 SSH 私钥和一份统计私钥；写 `inventory/nodes.json` 和公开订阅目录 |
| sync | `sync` | 只有统计 SSH 私钥；读 inventory，写 traffic.db 与 `telemetry/traffic.json`、`telemetry/history/` |
| subscriptions | `subscriptions` | 只读公开订阅目录，不接触控制库和主密钥 |

### 主控到节点：只有受限 SSH 固定命令

`transport.go` 的 `SSH.Call` 通过 `/usr/bin/ssh` 以 stdin/stdout 传 JSON。节点上两个账号被 `authorized_keys` 的 `from=,restrict,command=` 固定到入口脚本（`ops/controller/node/*-entry.sh`）：发布账号进入 `sbctl-node rpc`，统计账号进入 `sbctl-node stats-rpc`。管理地址必须是 Tailscale 地址。节点侧唯一的动作表是 `node_inspection.go` 的 `NodeRPC`（`status`/`submit`、`inspect`、`configure`、`diagnose`、`protocols`、`protocol_change`、`config`、`config_change`、`restart`、`maintenance_receipt`、`config_snapshot`、`adopt_config`）；不存在任意远程命令，新增能力意味着新增一个受校验的动作，并且主控与节点工具要一起升级。

### 三条链路

1. **授权下发**（新增/禁用/启用用户、调整授权）。`Control.*`（`control.go`、`user_grants.go`、`user_management.go`）在一个写事务里完成幂等重放、改写 users/credentials/revocations，并写入 `operations` + `operation_targets` + `outbox`。主控 worker 的 `Step` 领取 outbox 租约，为该节点生成**完整期望状态**的 `NodeRequest`（全部有效凭据 + 全部撤销指纹，单调 `node_revision`，`payload_hash` 封印；内容相同则复用旧请求），经 SSH `submit`。节点 `Agent.Accept` 持久接收，`sbctl-node worker` 循环 `ApplyOnce`：渲染模板 → Docker 预检 → 写 `config.json` 与启动守卫 → 重建 sing-box 容器 → 用真实协议探针验证（新凭据可用、被撤销凭据不可用）→ 回执 `verified`。主控轮询到 `verified` 后由 `Reconcile` 发布订阅并汇总操作状态。
2. **节点维护**（接管、协议入口增改启停删、配置片段修改、受控重启）。`POST /api/v1/node-jobs` 写入 `node_jobs`，worker 的 `RunNodeJob` 执行；节点侧 `NodeMaintenance.apply`（`protocol_management.go`，配置片段见 `config_management.go`）先落盘包含全部受影响文件前后内容的计划，再写入、重启、探针验证，失败自动回滚并保留回执。它与同一节点的授权链路互斥：提交时有待处理授权返回 `NODE_BUSY`，执行时遇到则保持排队；靠 `node_management.version`、`management_job_id` 和节点的 `maintenance_receipt` 做丢回执与中断后的恢复。新增一种会改动节点的任务类型时，SQL 里的 `kind IN ('configure','protocol_change','config_change','restart')` 列表、`mutatingNodeJob` 和 `transport.go` 的超时列表都要同步。
3. **流量统计**。节点 `sbctl-node collect` 每 5 秒用 grpcurl 读取 sing-box V2Ray API 计数（`reset:false`），`Ledger.Ingest` 按容器运行周期（epoch）把增量累加进 ledger.db 并导出 `export.json`。主控 `sync` 用统计账号拉取，`Traffic.Ingest` 校验序号、累计不回退、统计身份不变后写入 traffic.db，并按 UTC+8 记录日增量和小时增量（小时保留约 62 天，按月趋势由日增量折叠）。API 侧 `Views`（`views.go`、`traffic_history.go`）只读这些导出文件。统计有三个互不相加的角度：受管用户（`usr_*`）、入口（inbound tag）、节点本地命名用户。

在这个系统里，**配置生效等于重建 sing-box 容器**：没有热重载，任何授权或协议变更都会让该节点的现有连接短暂中断。容器入口是 `guard.sh`，只检查受控安全状态和版本格式；配置语义摘要由节点 helper 在启动后观察并按事件序号同步，避免格式化或手工修改非受管字段阻止 sing-box 启动。

每个节点受管的入口固定为两个（一个 AnyTLS、一个 TUIC），用户授权、订阅和探针都绑定在这两个入口上；其余入口只做维护与统计。

### 非显而易见的代码约定

- **错误用 panic 传递**。`fail(code, message, status)` 抛出 `*Fault`，`check(err)` 抛出原始错误；`Attempt(fn)` 是操作边界，把它们还原为 `error`（运行时 panic 不吞）。HTTP 层的 `protect` 把 `Fault` 变成 `{code,message}`，其它错误统一成 503 `SERVICE_ERROR`。新代码沿用这套写法，不要改成返回 `error`。
- **数据库**。`transaction(path, write, fn)` 每次新开连接并校验 `schema_version`；`DB.Rows/One/Value` 返回 `M`（`map[string]any`），取值用 `str`/`integer`/`boolean`/`mapping`/`array`。四个库的 schema 在 `internal/sbctl/schema/*.sql`（control、traffic、ledger、node），只有 `CREATE TABLE IF NOT EXISTS`，**没有迁移机制**；`schemaVersion` 不一致直接拒绝打开。已上线库要保留数据，所以加新表可以，改已有表的列需要自己设计兼容方案（现有做法是旁路表，如 `user_management`、`node_management`，或 `meta` 键）。
- **变更型 API** 都要求会话 Cookie + `Origin` 白名单 + `X-CSRF-Token` + `Idempotency-Key`（16–128 位）。`Control.replay`/`remember` 在同一事务里实现幂等；并发保护靠 `revision`/`version`，冲突返回 409 `STALE_REVISION`。
- **机密**。代理凭据、订阅 token、节点请求体经 `Vault`（AES-GCM，主密钥文件）加密入库，token 只存哈希；响应里的字节计数是十进制字符串。
- JSON 输入统一走 `decode`（拒绝重复键、未知字段、超过 2 MiB），输出走 `canonical`；落盘一律 `atomicWrite`，跨进程互斥用 `fileLock`。
- **版本号**。`common.go` 的 `Version`、`contracts/openapi.json` 的 `info.version`、`compose.yaml` 的默认镜像标签必须一起改，且与发布 tag 相同（CI 会比对 `sbctl version`）。
- 源码风格紧凑（Go 与 TS 都有很长的行、单文件多组件）。保持现状，不要顺手格式化或重排无关代码。

### API 契约

`contracts/openapi.json` 是唯一契约并嵌入二进制。Go 测试用 JSON Schema 校验真实响应；前端类型由它生成。改接口时四处要同步：`openapi.json`、Go handler（`api.go`）、`pnpm api:generate` 的产物、以及 `web/src/demo/transport.ts` 的演示实现。`scripts/update-node-contract.mjs` 是向契约批量追加节点/管理 schema 的补丁脚本，会原地重写 `openapi.json`。

### 前端

React 19 + Vite + TanStack Router/Query/Table + shadcn Base UI + Tailwind 4 + Recharts。`src/api/client.ts` 是唯一 API 出口：开发模式且 `VITE_DEMO_MODE=true` 时动态加载 `src/demo/transport.ts`（内存模拟后端），生产包只请求同源 `/api/v1`。`scripts/check-production.mjs` 会扫描 `dist`，发现演示标记即构建失败，所以演示代码只能经由 `import.meta.env.DEV` 分支到达。浏览器测试大多跑在演示数据或 HTTP mock 上，新接口不补演示实现会同时弄坏 `pnpm dev` 和 e2e。列表与流量筛选状态保存在 URL search 参数中（`router.tsx` 的 `validateSearch`）。

## 发布与部署

推送 `vX.Y.Z` tag 触发 `.github/workflows/release.yml`：两种架构的原生 runner 上跑 `make check`、前端检查、e2e 和镜像冒烟，再发布 GHCR 镜像与 GitHub Release。已发布的 tag 不移动，失败用新的补丁版本。流程与升级步骤见 `ops/controller/release.md`，生产目录、服务和节点准备见 `ops/controller/README.md`。

部署、节点任务和代理重启都作用于真实服务器与真实用户连接，属于需要用户明确确认的操作，不随代码修改自动进行。

## 文档约定

- `plan.md`：执行计划，每轮工作一节（范围、勾选步骤、停止条件、完成记录）。
- `docs/qa/<主题>-<日期>/report.md`：每轮的验收报告、回执和截图。
- `README.md`、`ops/controller/README.md`、`docs/management.md`：记录当前已部署版本、功能状态和使用说明，功能或版本变化后同步更新。
- `docs/product-development-plan.md`：产品方案、数据模型与里程碑，是理解设计取舍的首选入口。
- 提交信息沿用历史写法：英文 Conventional Commits（`feat:`、`fix:`、`docs:`、`ci:`），功能提交之后通常跟一条记录部署与验收的 `docs:` 提交。
