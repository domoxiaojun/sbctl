# ovhus Go 主控

2026-11-15 按用户要求，主控 API、CLI、worker、同步进程、订阅读取、节点发布及采集统一使用 Go，已在 ovhus 与 yxvmhk 部署并通过真实新增、禁用和订阅验收。旧 sbctl 数据不迁移；Go 数据库使用 schema v2，拒绝打开旧 schema v1。完整结果见 [迁移验证报告](../../docs/qa/go-migration-20261115/report.md)。

## 服务结构

主控在 ovhus（100.70.0.9），由 `/opt/sbctl/compose.yaml` 管理四个服务：

| 服务 | 命令 | 职责 |
| --- | --- | --- |
| api | sbctl serve | 前端静态文件、登录与管理 API |
| worker | sbctl worker | 持久任务、节点授权及订阅发布 |
| sync | sbctl sync | 拉取并持久保存节点统计快照 |
| subscriptions | sbctl subscriptions | 只读订阅文件，不持有控制库和主密钥 |

worker 与 sync 默认各以 4 路并行处理不同节点，可用 `--parallel N`（1–32）调整。同一节点的授权任务仍然逐个执行，改动节点的维护任务与该节点的授权任务互斥；worker 始终留一路给授权与撤销，节点任务最多占用 N-1 路，因此一台节点的长时间维护或失联不再拖住其它节点的撤销。sync 并行拉取、串行入库。需要恢复逐台串行时在 Compose 的 `command` 中加 `--parallel 1`。

当前发行目标为 `ghcr.io/domoxiaojun/sbctl:v0.10.6`，包含静态 Go 程序及 React 生产包。源码仓库和 GHCR 包保持私有；发布时由 CI 生成双架构程序、固定镜像 digest 和校验文件。镜像运行时没有 Python、uv 或 Node.js。主控使用标准库 HTTP 服务，SQLite 由纯 Go 驱动嵌入，无独立数据库服务。[发布及升级流程](release.md)、[管理功能验收](../../docs/qa/management-20261116/report.md)。本版本新增守卫收窄和双向配置同步，要求主控、受管节点工具和 guard 配套升级，保留代理内核和现有账本；先替换 guard，再启用新版节点 worker。程序替换本身不重启代理，执行协议配置或受控重启任务会短暂中断连接。生产仍为 v0.10.5，本次发行不包含服务器升级或多节点实机验收。

| 请求 | 后端地址 |
| --- | --- |
| 页面及 `/api/v1` | `http://127.0.0.1:18100` |
| Tailscale 页面直连 | `http://100.70.0.9:18100` |
| `/s/` 订阅路径 | `http://127.0.0.1:18101`，保留完整路径 |

HTTPS 站点由用户部署。应用仍允许来源 `https://sbctl.domob.org`，会话 Cookie 带 Secure 属性，浏览器登录必须通过该 HTTPS 来源。HTTP 内网直连可查看页面和健康状态，但不支持登录。

登录限流在 15 分钟窗口内按三层计数：同一来源对同一账号 10 次、同一来源 30 次、同一账号全部来源合计 100 次。单个来源耗尽的只是自己的额度，不会把管理员从其它地址锁在外面。API 默认以 TCP 对端作为来源；经反代访问时所有人都表现为反代地址，需要在 `server.toml` 设置 `trusted_proxies`，填写反代到达 API 时的地址或网段，并确认站点的根反代带有 `proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`。反代经 `127.0.0.1:18100` 访问时，API 看到的通常是 Compose 网络的网关，可在 ovhus 用 `docker network inspect sbctl-control_default --format '{{(index .IPAM.Config 0).Gateway}}'` 查看。只有对端属于该列表时才读取转发头，并且只采用反代追加的部分。该配置项随本次修复加入；回退到 0.10.4 及更早的程序前须先删除，否则旧程序会拒绝未知配置项。未配置时仍按对端地址计数。

2026-11-16 真实验收发现，现有站点仅将根路径转发到 18100，导致 `/s/` 返回“页面不存在”；内部订阅服务 18101 正常。用户明确选择继续自行管理站点。本仓库提供 [OpenResty 订阅反代配置](sbctl-subscriptions.nginx.conf)，已在 ovhus 的 OpenResty 容器中使用隔离临时配置通过 `nginx -t`，未改动或热加载现有站点。由用户添加至 `/opt/1panel/www/sites/sbctl.domob.org/proxy/sbctl-subscriptions.conf`，再校验并热加载 OpenResty。`proxy_pass` 地址末尾不加 `/`，以保留完整的 `/s/令牌/文件名` 路径；页面与管理 API 仍使用已有根反代。公网订阅可读性待用户配置后验证。

## 文件与身份

主控与节点的宿主部署统一放在 `/opt/sbctl`：`bin/` 是程序与固定 helper，`config/` 是配置和密钥，`data/` 是持久数据，`backups/` 是隔离备份，`releases/` 保存发布材料。主控 Compose 位于根目录的 `compose.yaml`。systemd 与 sudoers 留在系统标准目录；`/usr/local/bin` 和 `/usr/local/sbin` 中的 sbctl 入口仅为指向 `bin/` 的软链接。

2026-11-15 目录迁移已完成，密码、主密钥、数据库和节点账本均核对一致。随后通过网页节点管理流程重新检查并接管 yxvmhk；用户修改的容器名 `sing-box`、镜像 `ghcr.io/domoxiaojun/sing-box:v1.12.9-v2ray-api-2` 和其它代理入口均保留，仅把启动守卫挂载从旧路径对齐到 `/opt/sbctl/data/safety`。

- 容器配置：`/opt/sbctl/config/server.toml`。
- 宿主维护配置：`/opt/sbctl/config/server.host.toml`，使用宿主数据路径。
- 数据：`/opt/sbctl/data/control`、`traffic`、`telemetry`、`inventory`、`publication`。
- 主密钥：`/opt/sbctl/config/master.key`，32 字节随机密钥的 Base64url 编码，凭据使用 AES-GCM。
- 管理员：`admin`，密码使用 Argon2id 保存。新随机初始密码只写入 root 可读的 `/opt/sbctl/config/admin.initial`。
- Go 程序：`/opt/sbctl/bin/sbctl`。`sbctl version` 显示应用和 Go 版本，`sbctl doctor` 显示实际 SQLite 版本和数据计数。

需要更换管理员密码时，在 ovhus 执行 `SBCTL_CONFIG=/opt/sbctl/config/server.host.toml sbctl reset-password --password-file /opt/sbctl/config/admin.reset`（生成新的随机密码并写入该文件，文件须不存在），或用 `--password-stdin` 从标准输入提供至少 12 个字符的密码。重置会结束该账号的全部会话并清空登录限流计数。该命令随本轮改动加入，尚未发布。

通过已有维护连接读取初始密码：

```sh
ssh ovhus 'cat /opt/sbctl/config/admin.initial'
```

以下命令在 ovhus 执行：

```sh
SBCTL_CONFIG=/opt/sbctl/config/server.host.toml sbctl doctor
docker compose -f /opt/sbctl/compose.yaml ps
docker compose -f /opt/sbctl/compose.yaml logs --tail 30 worker sync
curl http://127.0.0.1:18100/healthz
curl http://127.0.0.1:18101/healthz
```

## 节点

yxvmhk 的 `/opt/sbctl/bin/sbctl-node` 提供固定 RPC、发布、恢复、运行绑定和采集命令。`sbctl-node.service` 以 root 执行发布；`sbctl-collector.service` 使用 sbctl-stats 用户采样并写入独立账本。两者直接执行 Go 程序。

发布与统计 SSH 密钥分开；固定入口继续校验 SSH_ORIGINAL_COMMAND。统计账号只允许调用固定运行状态 helper，不属于 Docker 组。采集使用现有 grpcurl 查询 `reset:false` 的 V2Ray 计数。

节点文件：`/opt/sbctl/config/node.toml`、固定模板及探针凭据；持久状态位于 `/opt/sbctl/data/node`、`ledger`、`safety`。用户授权管理覆盖登记的 AnyTLS/TUIC 入口；流量统计覆盖全部 sing-box 入口及可识别的命名用户。

### 流量统计范围

“流量统计”默认显示协议入口，可切换到“节点本地用户”。0.10.6 起不采集、不汇总、不暴露 sbctl 授权用户流量；yxvmhk 当前 19 个入口全部纳入统计，包含 AnyTLS、TUIC、VMess、VLESS、Shadowsocks 及 direct 转发。direct 表示本机转发入口，不推断它承载的加密协议或远端个人身份；不采集 VPS 网卡流量。

受控接管和后续授权发布会保留所有已有凭据、监听与路由，为无标签入口补充稳定 tag，为支持独立显示名的无名用户补充 name，并开启 V2Ray API 全入口和节点本地命名用户计数。系统探针不计入本地用户用量，但会计入入口总量。共享密码或无独立身份的连接只归属入口；节点本地用户不会因此获得 sbctl 管理授权。

入口与用户是两个统计角度，不能相加。新增统计项从首次采样建立基线，历史未采集流量不回填。已记录累计在重启、禁用和移除后保留；采样缺口继续显示，不能把它当成零流量。新字段通过原节点账本的增量表保存，升级不清空已有用户历史。

`GET /api/v1/traffic` 使用管理员会话，返回入口/本地用户累计、起点、新鲜度与质量。采集器经固定 root helper 获取不含凭据的统计清单，统计账号无须读取 sing-box 配置。回归测试同时覆盖 gRPC 对零值省略 `value` 的格式。

0.7.0 增加节点、协议、关键词和记录状态筛选，以及 UTC+8 日期区间查询。主控在流量库内保存日期增量，并导出到 `/opt/sbctl/data/telemetry/history/` 供 API 只读查询；首次基线为 2026-11-16 13:55:46（UTC+8）。旧累计继续保留，升级前日期无明细，不能反推或按零判断。网页每 15 秒读取最新统计，也支持手动刷新。0.8.0 基于相同日期账本提供日趋势及分类排行、点击筛选，保留原日期基线。0.9.1 将这些统计统一集中在流量统计页；总览显示运行摘要，节点页集中 sing-box 管理、配置检查和当前协议清单。节点详情可以定位进入对应流量查询，0.10.4 移除原用户详情的统计跳转。

小时粒度（未发布）：主控在流量库新增 `history_hours` 表，并在 `/opt/sbctl/data/telemetry/history/<节点>/<日期>.hours.json` 导出当天各小时的增量，供 `GET /api/v1/traffic?granularity=hour` 只读查询。小时明细从升级后的首次同步开始，保留约 62 天，过期的表记录和导出文件随新数据写入自动清理；日期明细不受影响、长期保留。新表由程序启动时自动创建，回退到旧程序时会被忽略。

```sh
# 以下在 yxvmhk 执行
systemctl status sbctl-node sbctl-collector
printf '{"action":"status"}' | /usr/local/sbin/sbctl-node-rpc
/usr/local/sbin/sbctl-runtime
# 维护排除错误后重试最新请求
sbctl-node retry
```

撤销预检失败沿用用户选择：保留旧服务，标记 `revoke_blocked` 并告警；启动清单保持阻塞。旧请求不能替代已接收的新版本，重启中断恢复会先核实运行周期，避免重复重启。

## 构建、安装与验证

在开发机运行 `make check`、`make build-linux`；前端按 web/package.json 构建。将 Linux 程序、`web/dist` 和部署文件传到服务器，在服务器构建 Dockerfile。Compose 的现有宿主绑定和站点由部署者确认，不在本机运行 Docker。

Go 维护入口包括 `sbctl initialize --password-file PATH`、`register`、`import-user`、`create-user`、`disable-user`；节点准备使用 `sbctl-node prepare`，由标准输入接收明确节点设置。程序拒绝覆盖已有配置或账本；全新初始化由维护流程显式处理旧状态。

节点管理页面支持从主控的只读 Tailscale 快照选择候选主机，也支持手动填写 Tailscale 地址。发现在线只代表主机可见，不代表已经接管。首次接入按页面指引安装固定 helper、配置 `sbctl-publish`/`sbctl-stats` 的受限 SSH 公钥，并核对 SSH 主机公钥；主控不会通过 API 执行任意 root SSH 或 Docker 命令。

点击检查后，worker 只读取 Compose、运行容器、配置摘要、AnyTLS/TUIC 入口、镜像能力、统计工具和现有用户数量；检查结果 15 分钟内有效。确认步骤可保留当前容器名和镜像引用，或指定新的合法名称/镜像；新镜像必须先被拉取、按摘要固定，并通过 `with_v2ray_api` 与配置预检。接管会备份文件、保留非受管入口和固定用户、写入启动守卫，必要时短暂重建当前容器，并用真实协议探针与独立采集通道验证。节点重启后会通过固定 `config_snapshot` 动作记录配置事件序号；主控发现手工修改时加密保存原始快照，并按节点锁吸收入口、凭据和订阅变化。未知用户身份保留快照且不部分更新；受管入口缺失、重复或无法唯一匹配则撤销该节点授权并暂停新增分配。

节点详情可以编辑名称、地区和分组，或重新打开更新绑定；版本号和并发任务保护避免覆盖其他管理员的修改。任务 ID 会保存在当前浏览器会话中，刷新页面后仍可查看排队、运行或失败结果；私钥和密码不会写入浏览器存储。主控只自动发现 Tailscale 主机，不自动接管其它节点。

配置同步增量尚未部署。升级时主控与节点工具、guard.sh 必须一起更新：先备份，再安装新 guard，再启用新节点 worker；不要让旧 guard 读取新的格式 2 清单。节点 worker 启动时迁移旧三行清单并将 safety 目录设为 root:容器 GID 0750、文件 0640；业务容器仍以只读目录挂载。启动标记继续记录已加载文件摘要，但不拿它否决启动。主控仅增加配置快照表，不重建现有数据库；节点的接管日志与观察序号需随 node 状态一起备份。

### 新节点准备

支持 Linux、systemd、Docker Compose 的 `sing-box` 服务，使用 host 网络、明确的非 root 数字 UID:GID；配置目录只读挂载为 `/etc/sing-box`，数据目录可写挂载为 `/var/lib/sing-box`。需要各一个 AnyTLS/TUIC 入口及有效 TLS 证书。公网地址是客户端连接地址；证书域名不同则在页面填写 TLS 证书域名。

将对应架构的 `dist/linux-<架构>/sbctl-node` 安装到 `/opt/sbctl/bin/sbctl-node`，把 `ops/controller/node` 完整目录传至节点 `/opt/sbctl/install/node`。将经过发布方 checksum 核验的 grpcurl 安装为 `/opt/sbctl/node-tools/grpcurl`，然后以 root 执行：

```sh
bash /opt/sbctl/install/node/bootstrap.sh
```

脚本安装账号、固定 helper、proto、systemd 服务与 guard，不启动或修改代理。两账号 home 可通过 `getent passwd sbctl-publish sbctl-stats` 查询。在各自 `.ssh/authorized_keys` 添加页面给出的对应公钥，使用下面的限制前缀；替换 `<公钥>`，不可写入私钥：

```text
from="100.70.0.9",restrict,command="/usr/local/bin/sbctl-publish" <发布公钥>
from="100.70.0.9",restrict,command="/usr/local/bin/sbctl-stats" <统计公钥>
```

每行放入对应账号文件，确保 home/`.ssh`/`authorized_keys` 属于该账号，`.ssh` 为 0700，`authorized_keys` 为 0600。通过可信维护连接读取 `/etc/ssh/ssh_host_ed25519_key.pub`，填入网页高级设置。主控 worker 的统计密钥副本为 `worker_stats_ed25519`（10001:10001，0600），同步进程使用 `stats_ed25519`（10002:10002，0600）；轮换统计密钥时同时更新两份。

Tailscale 发现由宿主的 `sbctl-discovery.timer` 每 30 秒写入 `/opt/sbctl/data/discovery/tailscale.json`，API 只读该快照。手动填写地址不依赖发现服务。

实时验收命令在 ovhus 执行，会新增专用测试身份并在结束时禁用，验证登录、新增发布、采集同步、订阅下载、禁用与订阅失效；添加 `--lifecycle` 时继续验证重新启用、新旧链接、原请求恢复及再次禁用：

```sh
sbctl acceptance --base-url http://127.0.0.1:18100 \
  --public-url http://127.0.0.1:18101 --origin https://sbctl.domob.org \
  --password-file /opt/sbctl/config/admin.initial --group grp_hk \
  --report /opt/sbctl/data/go-acceptance.json
```

真实代理认证和撤销探测由节点执行器执行；API 的 completed 需要匹配当前版本的 verified 回执。测试通过不代表用户端 Surge/Mihomo 已人工导入，也不替代多节点故障矩阵。

本次切换后受管业务用户为 0，验收身份已禁用；旧用户和历史流量未迁移。旧程序及状态隔离存档于 `/opt/sbctl/backups/go-transition-20261115`，不参与运行。旧初始密码已失效，读取新生成的密码文件。
