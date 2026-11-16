# GHCR 版本发布

源码仓库为 `domoxiaojun/sbctl`，发行镜像为 `ghcr.io/domoxiaojun/sbctl`。镜像包含 Go 主控、节点程序和 React 生产页面，分别在原生 Linux amd64/arm64 runner 构建验证。代理内核 `ghcr.io/domoxiaojun/sing-box` 单独发布，不随主控更新自动升级。

## 发行流程

1. 同步 `internal/sbctl/common.go`、OpenAPI 的版本及 Compose 默认标签。
2. 更新执行计划，按修改类型运行必要检查。推送同版本 `vX.Y.Z` 标签触发 `.github/workflows/release.yml`。
3. CI 执行 Go 竞争检测、前端契约/测试/构建、amd64 浏览器流程、两种架构镜像启动检查，再推送各架构镜像。
4. 两种架构都通过后发布版本标签和 `latest`，GitHub Release 提供静态程序包、SHA-256 校验文件及记录镜像 digest/源码提交的 `release.json`。

工作流只在 `domoxiaojun/sbctl` 的版本标签运行；所有 Actions 固定提交，发布权限限制在相应 job。构建上下文仅包含指定程序和 `web/dist`，不包含配置、密钥、数据库、运维快照或源码。不要移动已发布版本标签；失败需修改代码时使用新的补丁版本。

## 升级现有主控

以下操作仅在 Linux 主控执行，本机不运行 Docker。

先核对发行回执及 SHA-256，拉取精确 digest。停止相关写入服务后备份 Compose、现有程序、配置和数据。保留原镜像；无需重新初始化管理员或数据库。将 `/opt/sbctl/.env` 中的 `SBCTL_IMAGE` 设为经过验证的 `ghcr.io/domoxiaojun/sbctl@sha256:…`，Compose 使用该覆盖值启动四个服务。

升级节点时只替换 `/opt/sbctl/bin/sbctl-node` 并重启 sbctl 的发布/采集服务。保留代理 Compose、模板、探针凭据、节点安全版本和累计账本；单独通过节点管理检查/确认验证绑定。程序升级不直接重建 sing-box。

程序故障可以恢复旧程序和旧镜像。若已经执行了授权或撤销，不能用旧数据库/节点安全状态覆盖新记录；安全状态、撤销意图与配置须保持一致后再恢复。

私有 GHCR 且部署者未配置 `read:packages` 凭据时，可手动运行 `export-image.yml`，传入已发布版本。该流程使用 CI 的仓库包权限读取发行回执中的精确 digest，导出 amd64 镜像及 config ID/SHA-256。通过现有仓库权限下载 `deployment-image` 后传至 Linux 主控，核验归档摘要，使用 `docker load` 导入并核对 image config ID。此时 Compose 使用该已验证版本标签，启动指定 `--pull never`；镜像内容与 GHCR 相同，不把 GitHub 登录凭据写到服务器。

## 单节点验收

现有验收命令加 `--lifecycle`，使用同一测试身份完成新增、真实代理流量、禁用、重新启用及再次禁用。包含幂等重试、原请求恢复、身份/历史保留、新旧订阅状态和当前授权组预览。测试结束后测试用户禁用，订阅不可读取。

```sh
sbctl acceptance --base-url http://127.0.0.1:18100 \
  --public-url http://127.0.0.1:18101 --origin https://sbctl.domob.org \
  --password-file /opt/sbctl/config/admin.initial --group grp_hk \
  --lifecycle --report /opt/sbctl/data/release-acceptance.json
```

节点 verified 回执包含新凭据的 TCP/UDP 实际认证、移除凭据的失败探测与有效凭据对照。只有一台受管节点时，授权预览验证当前组与节点一致；不声称已完成跨节点增减或失联恢复验收。
