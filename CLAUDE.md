# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目定位

sbctl 是自托管的 sing-box 集群控制端：集中管理多台 VPS 上的 sing-box 节点（接管、协议入口维护、受控重启）、用户授权与 Surge/Mihomo 订阅、以及多角度累计/按日流量统计。源码仓库和 GHCR 镜像均公开，镜像支持匿名拉取。后端是 Go（主控单二进制），控制台是 React。当前生产范围是 **ovhus 主控 + yxvmhk 单节点**，多节点推广尚未验收。

## 常用命令

Go（仓库根目录，Go 1.27.3）：

```sh
make test                 # go test ./...
make check                # go vet + go test -race，CI 的必过项
make build-linux          # dist/linux-{amd64,arm64}/sbctl，CGO_ENABLED=0 静态主控程序
go test ./internal/sbctl -run '^TestName$' -count=1 -v    # 单个测试
go run ./cmd/sbctl help   # 主控子命令
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

### 单主控二进制 + 单容器内嵌 sing-box 监管器（零 SSH）

`cmd/sbctl` 是主控唯一二进制入口（原宿主机 `cmd/sbctl-node`、SSH 固定命令、双 Linux 账号与 `guard.sh` 已彻底移除）。受管 VPS 只需运行单个自研 `sing-box` 容器（`ghcr.io/domoxiaojun/sing-box:latest`，设置 `SBCTL_CONTROL_URL` 与 `SBCTL_NODE_ID`，首次用一次性 `SBCTL_REGISTRATION_CODE` 自动换取持久化 Token）。

主控默认以两个最小隔离 Compose 服务运行（`ops/controller/compose.yaml`）：

| 服务 | 子命令 | 读写 |
| --- | --- | --- |
| api | `serve` | 合并管理 API、内嵌后台 `Worker`、`AgentHub` 内存信道唤醒与 SSE 实时流推送；持连接池化的 WAL 模式 `control.db` 与 `traffic.db` |
| subscriptions | `subscriptions` | 只读公开订阅目录，不接触控制库和主密钥 |

### 主控到节点：零轮询 AgentHub 长连接信道

受管 `sing-box` 容器内的 `common/sbctlagent.Supervisor` 经 Tailscale 主动连接 `/api/v1/agent/{register,poll,receipt,telemetry}`。`control_agent.go` 的 `AgentHub` 使用 Go 内存 channel 在命令入队和回执到达时瞬时唤醒（零 SQLite 轮询空转），不再存在 SSH 回退通道。
- **零停机用户变更**：仅新增/更新 AnyTLS 或 TUIC 用户凭据时，调用协议层 `UpdateSBCTLUsers` 在内存中原子更新，无需中断现有连接。
- **进程内热重载与自动回滚**：修改协议端口、路由或 DNS 等结构性配置时，先将当前运行周期的累计流量与连接增量落盘至容器内 `DurableLedger`，再在进程内预检并热替换 `Box` 实例；若新实例启动失败则自动回滚旧实例。
- **实时速率与多维统计**：`common/sbctlstats` 在内存中维护滑动窗口上下行速率（`upload_bps`/`download_bps`）与活跃连接快照；无管理员在线查看时每 5 秒上报，有 SSE 订阅者（`GET /api/v1/traffic/live/stream`）时自动切换至 1.5 秒高频推送。支持协议入口（`inbounds`）、出站分流（`outbounds`）与节点本地命名用户（`local`）三维度累计与小时/日/月趋势，连接明细 Top 排行榜默认按总字节排序（`rank_by=bytes`），并支持节点月流量账单配额（`node_billing`）与阈值告警。

每个节点受管入口可以为零个、一个或多个。用户授权、订阅和探针按稳定 `inbound_id` 绑定到已验证的 AnyTLS/TUIC 入口；其它协议仍可维护和统计，进入用户订阅前必须补齐对应协议的凭据、探针和渲染能力。

### 非显而易见的代码约定

- **错误用 panic 传递**。`fail(code, message, status)` 抛出 `*Fault`，`check(err)` 抛出原始错误；`Attempt(fn)` 是操作边界，把它们还原为 `error`（运行时 panic 不吞）。HTTP 层的 `protect` 把 `Fault` 变成 `{code,message}`，其它错误统一成 503 `SERVICE_ERROR`。新代码沿用这套写法，不要改成返回 `error`。
- **数据库**。`transaction(path, write, fn)` 每次新开连接并校验 `schema_version`；`DB.Rows/One/Value` 返回 `M`（`map[string]any`），取值用 `str`/`integer`/`boolean`/`mapping`/`array`。四个库的 schema 在 `internal/sbctl/schema/*.sql`（control、traffic、ledger、node），只有 `CREATE TABLE IF NOT EXISTS`，**没有迁移机制**；`schemaVersion` 不一致直接拒绝打开。已上线库要保留数据，所以加新表可以，改已有表的列需要自己设计兼容方案（现有做法是旁路表，如 `user_management`、`node_management`，或 `meta` 键）。
- **变更型 API** 都要求会话 Cookie + `Origin` 白名单 + `X-CSRF-Token` + `Idempotency-Key`（16–128 位）。`Control.replay`/`remember` 在同一事务里实现幂等；并发保护靠 `revision`/`version`，冲突返回 409 `STALE_REVISION`。
- **机密**。代理凭据、订阅 token、节点请求体经 `Vault`（AES-GCM，主密钥文件）加密入库，token 只存哈希；响应里的字节计数是十进制字符串。
- JSON 输入统一走 `decode`（拒绝重复键、未知字段、超过 2 MiB），输出走 `canonical`；落盘一律 `atomicWrite`，跨进程互斥用 `fileLock`。
- **版本号**。`common.go` 的 `Version`、`contracts/openapi.json` 的 `info.version` 必须一起改，且与发布 tag 相同（CI 会比对 `sbctl version`）。Compose 默认使用 `ghcr.io/domoxiaojun/sbctl:latest`，发行时同时更新版本标签和 latest；`SBCTL_IMAGE` 仍可覆盖为指定版本或 digest。sing-box 的 Compose 默认同样使用 latest，由独立镜像 CI 发布。
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
