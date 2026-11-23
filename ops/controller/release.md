# GHCR 版本发布

源码仓库为 `domoxiaojun/sbctl`，发行镜像为 `ghcr.io/domoxiaojun/sbctl`。镜像包含 Go 主控和 React 生产页面，分别在原生 Linux amd64/arm64 runner 构建验证。代理内核 `ghcr.io/domoxiaojun/sing-box` 单独发布，不随主控更新自动升级。

源码仓库与 sbctl GHCR 包自 2026-11-18 起均公开，`latest` 与已发布版本镜像支持匿名拉取，不需要配置 `read:packages` 凭据。

## 发行流程

1. 同步 `internal/sbctl/common.go` 和 OpenAPI 的版本；Compose 默认保持 `latest`，不随补丁版本修改。
2. 更新执行计划，按修改类型运行必要检查。推送同版本 `vX.Y.Z` 标签触发 `.github/workflows/release.yml`。
3. CI 执行 Go 竞争检测、前端契约/测试/构建、amd64 浏览器流程、两种架构镜像启动检查，再推送各架构镜像。
4. 两种架构都通过后发布版本标签和 `latest`，GitHub Release 提供主控静态程序包、SHA-256 校验文件及记录镜像 digest/源码提交的 `release.json`。

工作流只在 `domoxiaojun/sbctl` 的版本标签运行；所有 Actions 固定提交，发布权限限制在相应 job。构建上下文仅包含指定程序和 `web/dist`，不包含配置、密钥、数据库、运维快照或源码。不要移动已发布版本标签；失败需修改代码时使用新的补丁版本。

## v0.12.0-rc.1 候选版与迁移

本版单独发布主控。`-rc.N` 标签生成对应版本的双架构镜像、程序包和 GitHub 预发行版。稳定镜像 `latest` 继续指向稳定发行版。

1. 确认每个试点节点有支持注册、长轮询、回执和遥测的内嵌 Agent 镜像，并将该镜像的固定引用设为 `SING_BOX_IMAGE`。已发布的 sing-box `v1.14.4-sbctl.1` 只有统计 socket，无法接入此版本主控。配套镜像就绪前，现有节点应继续使用 v0.11.1 主控。
2. 在维护窗口停止旧 API、worker 和 sync 写入，备份主控控制库、流量库、完整数据目录、配置、节点账本和镜像摘要。WAL 数据库须在进程停止后连同已有 `-wal`、`-shm` 文件一并备份，或使用 SQLite 一致性备份。
3. 新 Compose 由 UID 10001 的 API 写入控制、流量、遥测、清单和发布目录。旧流量目录及其文件属 UID 10002，升级前须将流量和遥测目录及现有内容的所有权交给 UID 10001，保留订阅服务 UID 10003 的只读访问。替换 Compose 并停用旧独立 worker 和 sync 服务。通过 `SBCTL_IMAGE` 选择候选版的固定镜像。
4. 为已有节点从详情生成注册码，新节点使用快速登记。核验 Agent 在线、当前配置、流量采样和连接明细后再迁移下一台。新内核生成新账本时，用 `sbctl register-replacement-ledger NODE_ID OLD_LEDGER_ID NEW_LEDGER_ID` 明确关联旧账本，并用 `ledger-history` 核对。生产数据和代理流量的验收须在目标节点进行。

## 旧版主控升级记录（v0.11.x 及更早）

以下操作仅在 Linux 主控执行，本机不运行 Docker。

默认使用 `ghcr.io/domoxiaojun/sbctl:latest`。升级时核对发行回执及 SHA-256，拉取 latest 并记录实际 digest，停止相关写入服务后备份 Compose、现有程序、配置和数据，保留原镜像；无需重新初始化管理员或数据库。若 `/opt/sbctl/.env` 已有旧版本的 `SBCTL_IMAGE`，需要移除该项或改为 `ghcr.io/domoxiaojun/sbctl:latest`，否则它会覆盖 Compose 默认值。需要回退时仍可通过该变量选择指定版本或已记录的 digest。修改标签不会自动更新已运行的容器。

sing-box 的 Compose 默认使用 `ghcr.io/domoxiaojun/sing-box:latest`，可用 `SING_BOX_IMAGE` 覆盖。该标签由 sing-box 独立 CI 在双架构协议和统计检查通过后发布，保留 `sbctl 原生统计 socket`。sbctl 的 guard 和节点 helper 属于宿主工具更新，不在 sing-box 镜像内；内核升级后需重新检查并确认节点绑定的实际镜像 ID，不会通过标签变化自动更新绑定。原始试点的固定 digest、探针脚本和运行快照保留用于历史复现。

从 v0.10.5 升至 v0.10.6 时，先停止 sbctl 节点 worker 并备份节点状态和守卫，再用发行包中的 `guard.sh` 替换节点原 guard，最后替换 `/opt/sbctl/bin/sbctl-node` 并启用新版发布/采集服务。新版 helper 会把旧三行 manifest 迁移为格式 2；旧 guard 无法读取新清单，必须遵守先 guard、后 worker 的顺序。主控也须配套升级，才能读取新配置快照 RPC。保留代理 Compose、模板、探针凭据、节点安全版本、配置观察序号、接管日志和累计账本；单独通过节点管理检查/确认验证绑定。程序升级不直接重建 sing-box。本版本发行不代表已完成真实服务器升级和配置同步验收。

程序故障可以恢复旧程序和旧镜像。若已经执行了授权或撤销，不能用旧数据库/节点安全状态覆盖新记录；安全状态、撤销意图与配置须保持一致后再恢复。

离线部署或需要传输镜像归档时，可手动运行 `export-image.yml`，传入已发布版本。该流程使用 CI 的仓库包权限读取发行回执中的精确 digest，导出 amd64 镜像及 config ID/SHA-256。下载 `deployment-image` 后传至 Linux 主控，核验归档摘要，使用 `docker load` 导入。导入后再次 `docker save`，核对其中配置 JSON 的 SHA-256 与回执中的 config ID 一致；Docker 29 的 containerd 后端可能将本地 OCI manifest digest 作为 image ID，不能直接用该值和 CI 的配置摘要比较。此时 Compose 使用该已验证版本标签，启动指定 `--pull never`；镜像内容与 GHCR 相同。

## 单节点验收

现有验收命令加 `--lifecycle`，使用同一测试身份完成新增、真实代理流量、禁用、重新启用及再次禁用。包含幂等重试、原请求恢复、身份/历史保留、新旧订阅状态和当前授权组预览。测试结束后测试用户禁用，订阅不可读取。

```sh
sbctl acceptance --base-url http://127.0.0.1:18100 \
  --public-url http://127.0.0.1:18101 --origin https://sbctl.domob.org \
  --password-file /opt/sbctl/config/admin.initial --group grp_hk \
  --lifecycle --report /opt/sbctl/data/release-acceptance.json
```

节点 verified 回执包含新凭据的 TCP/UDP 实际认证、移除凭据的失败探测与有效凭据对照。只有一台受管节点时，授权预览验证当前组与节点一致；不声称已完成跨节点增减或失联恢复验收。

## v0.11.1 升级

本版使用捆绑自研统计的 sing-box 1.14.4-sbctl.1。生产升级时，从验证回执选择主控和内核的固定镜像摘要。旧试点的回退镜像和工具在对应试点目录保留。

1. 在维护窗口备份主控与节点的 SQLite、配置、Compose、启动守卫和密钥，保留旧镜像摘要。数据库迁移后回退旧程序需使用对应版本的备份，并先核对备份后的授权与撤销记录。
2. 将主控流量目录设为 `10002:10002`、`0750`。新版 Compose 让 API 以补充组 10002 只读挂载该目录，统计同步独占写入。同步进程启动后会将数据库设为 `0640`。
3. 配套更新主控和节点工具。先替换节点 guard，再启动新版 worker。通过「更新绑定」指定新内核固定 digest。接管在受控目录建立专用 Unix socket 挂载，并用实际采集身份检查读取权限。
4. 检查采样和中心同步，确认累计与历史仍可查。验证新建短连接出现在明细中，再验收 AnyTLS／TUIC、订阅和入口退役。节点滚动升级期间旧统计工具仍可提供累计快照。

本版连接明细保存已结束连接，正常采集下按 90 天保留。节点先写本地账本，主控通过 Tailscale 上的受限 SSH 分页补拉。内核缓冲被覆盖时标记连接历史缺口，累计统计独立继续。
