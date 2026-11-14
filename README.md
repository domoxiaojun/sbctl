# sbctl

集中管理 sing-box 节点、用户授权、累计流量与订阅的控制台。目前交付 **F0 前端工程与交互原型**，后端、节点管理和服务器部署按后续里程碑实施。

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
- 全局搜索、筛选、整数累计排序和分页，URL 保存列表状态。
- 添加用户和节点组选择、显式获取订阅链接、禁用确认、逐节点操作进度。
- 新增及禁用的响应丢失后找回原操作；仅暂存请求 key、操作类型和非秘密对象 ID。
- 浅色、深色、跟随系统主题，以及手机卡片列表和导航抽屉。
- 开发专用 `/components` 页面，包含表单、弹窗、表格、图表和加载／空／错误／过期状态。

演示域名为无效域名。展示的“已接收”“部分完成”“已完成”来自模拟状态，不代表实际 VPS 或代理客户端已经生效。

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

生产预览使用 `pnpm preview`，默认监听 `http://127.0.0.1:4186`。生产包请求同源 `/api/v1`，没有管理 API 时显示连接错误，不使用演示数据。部署时由服务器端 Caddy 提供静态页面、HTTPS 和 API 代理；当前尚未配置部署。

OpenAPI 草案位于 [contracts/openapi.json](contracts/openapi.json)，前端类型由 `pnpm api:generate` 生成。M1 接入后应以 FastAPI 生成契约为权威，并核对前端差异。

## 开发资料

- [产品开发方案与里程碑](docs/product-development-plan.md)
- [F0 验证与截图](docs/f0-validation.md)
- [开发方案审查记录](docs/development-plan-review.md)
- [执行计划](plan.md)

下一阶段先完成 M0 试点材料与能力验证，再进入真实控制领域层、API 和节点接入。重新启用、已有授权调整、凭证轮换、真实订阅发布、CLI 和服务器部署均属于后续实现。
