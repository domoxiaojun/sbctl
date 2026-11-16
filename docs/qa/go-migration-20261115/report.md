# Go 迁移验证记录

日期：2026-11-15（Asia/Singapore）。用户要求全面改为 Go，并明确现有 sbctl 数据不保留；本次采用全新初始化。Go 主控版本 0.3.0、Go 1.27.3、内嵌 SQLite 3.54.1、schema v2。

## 完成内容

- 主控 HTTP API、CLI、持久任务、统计同步、订阅读取迁移为 Go。
- 节点发布状态机、运行绑定、实际协议探针、流量账本和采集迁移为 Go。
- 原 Python 源码、测试、依赖清单和运维脚本已由 Go 实现替代。仓库不再需要 Python 环境。
- Linux amd64 和 arm64 静态程序已构建；实际部署为 amd64。镜像在 ovhus 构建，本机未运行 Docker。
- 主控四个容器和 yxvmhk 两个 systemd 服务都已切换。旧程序与状态移出运行路径，隔离放在服务器的 `/var/backups/sbctl/go-transition-20261115`，未导入新库。
- 旧管理用户、会话、订阅和流量历史未迁移；原受管 AnyTLS/TUIC 用户已清空，当前只有运维探针。19 个入口及未受管入口配置保留。
- 新随机管理员密码写入 `/etc/sbctl/admin.initial`；未写进仓库、镜像和本报告。

## 验证

| 项目 | 结果 |
| --- | --- |
| Go 测试 | 13 项顶层用例及撤销策略子用例通过 |
| 并发 | go test -race ./... 通过；重复提交仅创建一次、并发领取仅一方获得任务 |
| 静态检查 | go vet ./... 通过 |
| Linux 实机测试 | 同一组测试在 ovhus 隔离临时目录通过，见 linux-tests.log |
| API 契约 | 实际 Go 响应由 contracts/openapi.json 的 JSON Schema 校验；前端生成类型无漂移 |
| 前端 | 类型检查、生产构建、演示内容排除检查通过；登录按钮回归通过 |
| 浏览器真实后端 | 生产前端 + Go API：点击登录、刷新保留会话、进入总览、注销通过 |
| 认证与敏感数据 | Argon2id 登录、错误限流、CSRF、Secure/HttpOnly/SameSite、AES-GCM 篡改拒绝通过 |
| 流量账本 | 首次基线、重复采样、重启累计、计数下降/缺失冻结、快照乱序和账本换代拒绝通过 |
| 节点安全行为 | 旧请求不会恢复授权；撤销预检遵守 preserve/stop；进程中断恢复不重复重启 |
| 真实新增与禁用 | ovhus 提交、yxvmhk 接收并实际验证，两个操作均 completed，最终 revision 2 verified |
| 真实协议 | 新凭据和保留探针对 AnyTLS/TUIC 进行 HTTPS/UDP DNS 请求；禁用后旧凭据代理请求失败，有效探针对照通过 |
| 订阅 | Surge/Mihomo 下载成功，禁用后两个链接均为 404 |
| 统计同步 | 新节点账本与主控快照同步正常，freshness current；历史从新基线开始 |
| 部署文件 | shellcheck、hadolint、TOML 校验、服务器 Compose 校验通过 |
| 文档 | 修改文档 51 个链接检查通过 |
| 依赖 | OSV 定向扫描 go.mod，无已知漏洞报告 |
| 凭据检查 | gitleaks 脱敏扫描仅命中 config.go 中 SSH 密钥文件名默认值，非密钥内容 |

本轮没有重复 10% 流量误差的业务字节基准，也没有执行用户设备上的 Surge/Mihomo 人工导入。禁用引发新运行周期后，账本按现有规则记录一次潜在采样尾部缺口，不宣称零损失。

HTTPS 站点仍由用户管理。`http://100.70.0.9:18100` 可显示页面，浏览器登录仍要求 `https://sbctl.domob.org`。本次已部署登录按钮修复。

完整实机验收输出见 [Go 实机验收](../../../ops/controller/go-acceptance-20261115.json)，版本、镜像与程序摘要见 [部署状态](../../../ops/controller/runtime-20261115.json)。
