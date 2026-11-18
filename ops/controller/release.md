# GHCR 版本发布

源码仓库为 `domoxiaojun/sbctl`，发行镜像为 `ghcr.io/domoxiaojun/sbctl`。镜像包含 Go 主控、节点程序和 React 生产页面，分别在原生 Linux amd64/arm64 runner 构建验证。代理内核 `ghcr.io/domoxiaojun/sing-box` 单独发布，不随主控更新自动升级。

源码仓库与 sbctl GHCR 包自 2026-11-18 起均公开，`latest` 与已发布版本镜像支持匿名拉取，不需要配置 `read:packages` 凭据。

## 发行流程

1. 同步 `internal/sbctl/common.go` 和 OpenAPI 的版本；Compose 默认保持 `latest`，不随补丁版本修改。
2. 更新执行计划，按修改类型运行必要检查。推送同版本 `vX.Y.Z` 标签触发 `.github/workflows/release.yml`。
3. CI 执行 Go 竞争检测、前端契约/测试/构建、amd64 浏览器流程、两种架构镜像启动检查，再推送各架构镜像。
4. 两种架构都通过后发布版本标签和 `latest`，GitHub Release 提供静态程序及 `guard.sh` 包、SHA-256 校验文件及记录镜像 digest/源码提交的 `release.json`。

工作流只在 `domoxiaojun/sbctl` 的版本标签运行；所有 Actions 固定提交，发布权限限制在相应 job。构建上下文仅包含指定程序和 `web/dist`，不包含配置、密钥、数据库、运维快照或源码。不要移动已发布版本标签；失败需修改代码时使用新的补丁版本。

## 升级现有主控

以下操作仅在 Linux 主控执行，本机不运行 Docker。

默认使用 `ghcr.io/domoxiaojun/sbctl:latest`。升级时核对发行回执及 SHA-256，拉取 latest 并记录实际 digest，停止相关写入服务后备份 Compose、现有程序、配置和数据，保留原镜像；无需重新初始化管理员或数据库。若 `/opt/sbctl/.env` 已有旧版本的 `SBCTL_IMAGE`，需要移除该项或改为 `ghcr.io/domoxiaojun/sbctl:latest`，否则它会覆盖 Compose 默认值。需要回退时仍可通过该变量选择指定版本或已记录的 digest。修改标签不会自动更新已运行的容器。

sing-box 的 Compose 默认使用 `ghcr.io/domoxiaojun/sing-box:latest`，可用 `SING_BOX_IMAGE` 覆盖。该标签由 sing-box 独立 CI 在双架构协议和统计检查通过后发布，保留 `with_v2ray_api`。sbctl 的 guard 和节点 helper 属于宿主工具更新，不在 sing-box 镜像内；内核升级后需重新检查并确认节点绑定的实际镜像 ID，不会通过标签变化自动更新绑定。原始试点的固定 digest、探针脚本和运行快照保留用于历史复现。

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
