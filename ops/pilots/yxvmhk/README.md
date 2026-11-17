# yxvmhk 容器试点运行记录

仓库中的 Compose 模板现默认使用 `ghcr.io/domoxiaojun/sing-box:latest`，可用 `SING_BOX_IMAGE` 覆盖；latest 由独立 CI 验证后发布。此修改尚未应用到服务器。下方固定 digest、版本校验脚本和验收结果仍是原始试点记录，后续内核升级需重新检查并确认 sbctl 节点绑定。

本页下文是 2026-11-14 的原始试点记录。2026-11-15 已部署 Go 0.5.0 持久账本与主控 API，现用容器名为 `sing-box`，镜像仍为 `ghcr.io/domoxiaojun/sing-box:v1.12.9-v2ray-api-2`，19 个入口及可识别用户均已纳入统计。当前运维路径和统计范围以[主控说明](../../controller/README.md)为准。

2026-11-14 已完成切换。服务器使用 `/etc/sing-box/compose.yaml`，网络为 `host`，直接由 Docker Compose 管理。原生 sing-box 1.12.9 软件包和 systemd 单元已卸载，PATH 中没有宿主 sing-box 命令；dpkg 只保留配置文件记录。

镜像为 `ghcr.io/domoxiaojun/sing-box:v1.12.9-v2ray-api-2` 对应的固定 digest，详见[镜像构建记录](../../images/sing-box/v1.12.9-v2ray-api-2.json)。容器名 `sbctl-yxvmhk-sing-box`，UID/GID 为 994，配置目录只读挂载，状态目录为 `/var/lib/sing-box`。Docker 服务已启用，容器策略为 `unless-stopped`；本次未重启整台 VPS。

## 运维命令

以下命令在 yxvmhk 上执行：

```sh
cd /etc/sing-box
docker compose ps
docker compose logs --tail 50 sing-box
docker compose restart sing-box
```

需要重新创建容器时，保留现有配置和数据目录，使用同一份固定镜像 Compose：

```sh
docker compose -f /etc/sing-box/compose.yaml up -d --force-recreate sing-box
```

只读查看统计，不重置计数：

```sh
/opt/sbctl/pilots/yxvmhk/read-stats.sh
```

`experimental.v2ray_api` 只监听 `127.0.0.1:10085`。保留现有用户名与凭据，AnyTLS 与 TUIC 的同名用户计数会合并；同时启用 `anytls-in`、`tuic-in` 两个入口计数，以便分别核对协议流量。TUIC 只增加入口 tag，原有路由与授权没有改变。

## 验证结果

- 19 个既有入口保留，36 个 TCP／UDP 配置监听全部与容器进程对应。
- AnyTLS、TUIC 的 HTTPS 测试均为 204，UDP DNS 查询均成功。
- 两协议的 TCP、UDP 测试均观察到用户和入口上下行计数增长。
- 统计 API 在宿主回环可用；经两协议访问其 IPv4 地址或 `localhost` 均无法取得实际 HTTP/2 响应。
- Compose restart 后保留容器 ID，StartedAt 变化；再次通过协议、统计和访问边界验证。
- 卸载原生包没有改变配置、Compose 或容器启动时间。卸载后再次核实运行状态、监听、镜像摘要和统计 API。
- [运行快照](runtime-20261114.json)记录最终容器与 Tailscale 状态。

首轮测试把 SOCKS CONNECT 的提前成功响应当成到达目标，导致自动回退；在原生服务上用临时回环统计服务复现并修正检测后，第二轮完整通过。自动回退曾实际执行成功。

## 备份与边界

成功切换备份位于服务器 `/var/backups/sbctl/yxvmhk/20261114T105125Z`，包括原配置、原服务单元、停写后的状态归档、原生二进制的只读备份、安装后摘要及测试记录。首次回退记录位于 `/var/backups/sbctl/yxvmhk/20261114T104413Z`。

`rollback.sh` 是迁移期间恢复原生服务的工具；原生包卸载后它会立即拒绝执行，避免误停正常容器。后续通过 Compose 维护；配置恢复须先核对当前授权，不能用旧备份整体覆盖后来变更的凭据。

当前 acme.sh 续期钩子只输出提示，未自动重启 sing-box。私钥为 `root:sing-box`、0640；证书更新后应执行上述 Compose restart，使新连接加载更新后的证书。此次没有强制签发新证书。

统计目前仍是 sing-box 内存计数，重启后进入新的统计周期；持久账本、后台真实 API、受限 SSH 工具及 Tailscale 权限矩阵尚未完成，不能把当前计数当作已完成的持久计费数据。
