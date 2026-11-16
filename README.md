# sbctl

集中管理 sing-box 节点、用户授权、累计流量与订阅的控制台。主控和节点工具统一使用 **Go**，前端使用 React。部署范围为 **ovhus 主控 + yxvmhk 单节点**；HTTPS 入口与访问范围由用户自管站点控制。部署、登录和验收结果见 [主控说明](ops/controller/README.md)。

## 本地运行

使用仓库 `.node-version` 指定的 Node.js 26.12.1 和 `web/package.json` 指定的 pnpm 12.18.0：

```sh
cd web
pnpm install --frozen-lockfile
pnpm dev
```

打开 `http://127.0.0.1:5186`。开发模式由 `web/.env.development` 显式启用演示数据，页面顶部持续显示演示标记；操作只改变当前标签页的模拟状态。注销后的演示登录接受任意非空内容，请勿输入真实密码。

若本机 pnpm 版本不同，可使用已验证的固定版本入口，例如 `npm exec --yes --package=pnpm@12.18.0 -- pnpm dev`。不需要本机数据库或 Docker。

## 当前可用内容

- 总览、节点与用户列表／详情、流量统计、操作记录、告警与主题设置。
- sing-box 全入口累计、协议汇总、sbctl 用户与节点本地命名用户用量；显示统计起点和数据质量，匿名/共享密码流量只归属入口，不统计 VPS 网卡。
- 节点 Tailscale 发现或手动添加、只读检查与确认接管、名称/地区/分组编辑、镜像和容器名更新、运行与采样诊断；[接入和维护说明](ops/controller/README.md)。
- 全局搜索、筛选、整数累计排序和分页，URL 保存列表状态。
- 添加用户和节点组选择、显式获取订阅链接、禁用确认、逐节点操作进度。
- 新增及禁用的响应丢失后找回原操作；仅暂存请求 key、操作类型和非秘密对象 ID。
- 浅色、深色、跟随系统主题，以及手机卡片列表和导航抽屉。
- 开发专用 `/components` 页面，包含表单、弹窗、表格、图表和加载／空／错误／过期状态。

本地演示域名为无效域名，演示进度来自模拟状态。服务器生产包使用真实管理 API，节点应用、协议验证与订阅发布分别确认。

当前界面采用暖白／石墨黑日夜主题，以赤陶橙和紫灰作为强调色；导航、选择器、表单、提示与图表使用 shadcn Base UI 组件。组件网站、配色与新截图见[界面重做记录](docs/ui-refresh.md)。

## 验证与生产构建

```sh
cd web
pnpm api:check
pnpm test
pnpm lint
pnpm build
pnpm exec playwright install chromium
pnpm e2e
```

`build` 包含 TypeScript 检查及演示内容排除检查；输出在 `web/dist`。浏览器测试会自行使用 `15186` 和 `14186` 端口启动开发版与生产预览，拒绝复用已有服务。

生产预览使用 `pnpm preview`，默认监听 `http://127.0.0.1:4186`。生产包请求同源 `/api/v1`，没有管理 API 时显示连接错误，不使用演示数据。ovhus 的站点由用户自行部署，原 sbctl 专属站点配置已撤销；API 服务提供前端文件，四个 Compose 服务分别处理管理、操作、统计同步和订阅读取，后端回环端口保留。反代地址见 `ops/controller/README.md`。

后端使用 Go 1.27.3、标准库 HTTP 服务和纯 Go SQLite 驱动；生产程序无 Python 或 CGO 运行依赖。两个入口分别为 `cmd/sbctl` 和 `cmd/sbctl-node`，共享逻辑位于 `internal/sbctl`。

`contracts/openapi.json` 是独立于语言的 API 契约；Go 测试用 JSON Schema 校验实际响应，前端通过 `pnpm api:generate` 生成类型、`pnpm api:check` 检查漂移。`go run ./cmd/sbctl export-openapi /tmp/openapi.json` 可导出随程序嵌入的契约。

`make check` 执行 `go vet` 和竞争检测测试；`make build-linux` 生成 amd64、arm64 的静态程序。测试只使用内嵌 SQLite 临时文件，不安装数据库服务。本机不运行 Docker，镜像在 Linux 服务器构建。

2026-11-15 的 Go 切换按用户要求全新初始化数据，旧 Python 数据库不迁移，schema v2 拒绝误读旧库。此后的 0.6.1 升级保留现有数据库、密码、密钥和账本；初始密码文件仍为 `/opt/sbctl/config/admin.initial`。完整部署方式见 [主控说明](ops/controller/README.md)。

## 开发资料

- [产品开发方案与里程碑](docs/product-development-plan.md)
- [F0 验证与截图](docs/f0-validation.md)
- [开发方案审查记录](docs/development-plan-review.md)
- [执行计划](plan.md)

当前已发布并部署 **v0.6.1**，GHCR 镜像为 `ghcr.io/domoxiaojun/sbctl:v0.6.1`，支持 Linux amd64/arm64。ovhus 与 yxvmhk 已更新，真实单节点检查、绑定、新增、禁用、重新启用、订阅和流量闭环通过。节点组授权调整已随版本上线；本次只验证了单节点当前组预览，跨节点增减仍待后续验收。直接节点授权、组成员变更自动传播、普通凭据轮换、多节点推广和生产恢复矩阵仍属于后续里程碑。见 [0.6.1 发布验收](docs/qa/release-v0.6.1-20261116/report.md)。

## 重新启用用户（0.6.1 已部署并通过单节点验收）

在已禁用用户的详情页点击“重新启用”，检查当前授权组与节点数量，填写原因后确认。保留原用户身份、创建时间、备注及历史用量，为当前授权节点生成新的 AnyTLS 密码和 TUIC UUID/密码；旧凭据的撤销记录继续保留。配置通过节点验证后发布新订阅，需要把新链接重新交付给用户。节点重启会造成短暂重连。

旧订阅链接在重新启用提交前再次吊销；无法吊销时拒绝本次启用，不生成可见的新授权。旧节点撤销尚未确认时，新任务仍携带全部旧凭据撤销记录；失联节点恢复后应用最新意图。页面区分“配置中”、逐节点验证和订阅发布，刷新或响应丢失时可找回原操作。

API 为 `POST /api/v1/users/{user_id}/enable`，请求体包含 `revision` 和 `reason`，要求有效会话、CSRF 与 `Idempotency-Key`。维护 CLI 使用同一领域逻辑：

```sh
sbctl enable-user USER_ID REVISION '恢复使用' REQUEST_KEY
```

`REVISION` 取当前用户版本，`REQUEST_KEY` 使用 16–128 位字母、数字、下划线或连字符；重试保留原 key、版本与原因。成功返回操作 ID，不代表节点已生效。此流程的本地验证与后续事项记录在 [执行计划](plan.md)。

## 调整已有用户授权（0.6.1 已部署，跨节点增减待验收）

在已启用用户详情点击“调整授权”，选择节点组后点击“预览授权影响”。确认页列出新增、保留与撤销的节点，填写原因后提交。多个组取节点并集，重叠组不会重复授权；仅移除一个授权来源时，仍由其它所选组授权的节点保留原凭据。至少保留一个有可管理节点的组；停止全部访问使用“禁用用户”。

新增节点生成独立凭据，撤销节点先进入高优先级队列，再次授权已移除的节点使用新代凭据。保留节点的相同配置请求会复用节点回执，不因组来源变化重新生成凭据或重启。用户身份与历史流量保留；失联节点持续重试，未完成的旧撤销会包含在新操作中。节点变更可能短暂中断连接。

订阅链接保持不变，内容仅包含仍授权且已经验证的节点；无就绪节点时暂时返回 404。订阅更新失败会告警和重试，不能阻塞节点撤销；失败期间旧订阅内容可能仍可读取。只有节点目标全部验证且订阅发布成功，操作才显示完成。预览后用户版本、节点组成员、目标绑定或待撤销范围变化时，提交会拒绝覆盖并要求重新预览。

API：`POST /api/v1/users/{user_id}/grants/preview` 接收 `revision`、`group_ids`，只读返回影响和 `preview_hash`；`POST /api/v1/users/{user_id}/grants` 另需 `reason`、`preview_hash` 和 `Idempotency-Key`。两者都要求有效会话及 CSRF。CLI 共用领域逻辑：

```sh
sbctl preview-grants USER_ID REVISION grp_hk,grp_sg
sbctl update-grants USER_ID REVISION grp_hk,grp_sg '调整使用区域' PREVIEW_HASH REQUEST_KEY
```

提交时使用预览输出的摘要，重试保留全部参数及原请求 key。直接节点授权、修改组成员后的自动传播不在此增量中；当前用户可重新预览并提交同一组选择以同步已经变化的组范围。验证记录见 [授权调整报告](docs/qa/user-grants-20261116/report.md)。
