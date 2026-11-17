# v0.10.5 发布与单节点验收

日期：2026-11-17，Asia/Singapore。范围：发布主控和节点工具，升级 ovhus 与 yxvmhk，检查 oraclesg 的接入条件；不升级 sing-box 内核，不改 oraclesg 的生产服务。

构建约束：正式编译、生产构建、镜像构建和浏览器构建验证均以 GitHub Actions 为准；本机不作为发行构建机，也不运行 Docker。

## 发布

- 源码提交：`63861be55b8495418c8f4ba38d7d61dca2844511`。
- Git tag：`v0.10.5`。
- GitHub Release CI：`44017051952`，amd64/arm64 构建、Go/前端检查、61 项 amd64 浏览器测试、镜像冒烟和 GHCR 推送通过。
- 日常 CI：`44016468022` 通过，包含 Go vet/竞态、Node 26 前端检查、25 项单元测试、生产构建和 actionlint。
- GHCR 多架构 digest：`sha256:58cc15db1ad15ca3814611355817766ef990b95040f1ea9d0f42d5fd9554dbe0`。
- 发布回执：`release.json` 记录版本、提交、digest 和 `linux/amd64`、`linux/arm64` 平台。

## ovhus 升级

- 升级前旧镜像 digest：`sha256:3a5e5901de4aae18506f2213eabf9582c2b4ac79b2a164f1a98ee0a1e826c59d`。
- 新镜像导入后通过容器内 `sbctl version`、镜像版本标签和源码 revision 校验；Docker `load` 后本地 image ID 不作为跨环境内容摘要比较依据。
- 备份：`/opt/sbctl/backups/release-v0.10.5-control-20261117T045018Z`，包含 Compose、配置、主密钥、控制/流量/遥测数据、程序和旧镜像检查信息。
- 四个服务均运行 `ghcr.io/domoxiaojun/sbctl:v0.10.5`，`/healthz` 返回 `{"status":"ok","version":"0.10.5"}`。
- 反代通过 OpenResty host 网络访问 API 的来源为 `172.24.0.1`；`server.toml` 已设置 `trusted_proxies=["172.24.0.1"]`，站点追加 `X-Forwarded-For`。
- yxvmhk 的 `sbctl-node` 和 `sbctl-collector` 均升级到 `0.10.5` 并保持 active；sing-box 运行周期未因程序升级改变。

## yxvmhk lifecycle acceptance

使用专用测试身份，最终保持禁用。`/opt/sbctl/data/release-acceptance-v0.10.5.json` 记录完整回执。

- 登录、创建、节点授权和实际协议验证通过。
- 禁用、旧凭据撤销、旧订阅 404 通过。
- 重新启用生成新凭据，新订阅可读；旧订阅继续 404。
- 再次禁用完成，新订阅失效。
- 幂等重试和响应丢失后的原操作恢复通过。
- 用户身份、节点账本、历史累计和现有入口保留。
- 报告版本为 `0.10.5`，节点工具和主控版本一致。

报告中的流量质量仍包含既有历史缺口；本轮没有把缺口当作零流量，也没有重新宣称 10% 误差对照达标。

## oraclesg 接入边界

oraclesg 的 Tailscale 地址为 `100.70.0.3`，但当前生产服务是原生 systemd `sing-box 1.13.11`，没有 `/opt/sbctl/bin/sbctl-node`、sbctl helper、统计 grpcurl 或已拉取的兼容 V2Ray API 镜像。现有 `/etc/sing-box/compose.yaml` 只是未运行的普通 sing-box Compose 文件。

按照本轮“不升级 sing-box 内核”的边界，没有把原生服务强行迁移到容器，也没有修改 oraclesg。双节点接入、失联/失败注入、双节点流量对照和多节点生产就绪因此保持未完成。
