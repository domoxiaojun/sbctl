# v0.10.6 发行回执

2026-11-17，按用户“提交发布新版构建新 docker”的要求提交源码并通过 GitHub Actions 构建和发布。本轮没有连接或升级 VPS，没有在本机运行 Docker 或执行编译。

## 发行标识

- 源码提交：`9a7a6378efd2f3c23719aafa7af1ac3a39516663`。
- 标签：`v0.10.6`，未移动已有版本标签。
- [GitHub Release](https://github.com/domoxiaojun/sbctl/releases/tag/v0.10.6)，发布时间 `2026-11-17T15:16:39Z`。
- 镜像标签：`ghcr.io/domoxiaojun/sbctl:v0.10.6` 和 `latest`。
- 固定镜像：`ghcr.io/domoxiaojun/sbctl@sha256:88ce5e55dba37fbc2450957d993d02201a9be65fb65a147ba0ce6c5e08fa8282`。
- 平台：`linux/amd64`、`linux/arm64`。

## 内容与升级要求

收窄配置启动守卫，增加重启后的双向配置同步、加密快照、凭据外部代数、撤销历史保留、并发保护和中断恢复；控制台显示节点配置同步状态。本版同时包含此前 main 已提交的受管用户流量统计分类移除，用户管理、授权、订阅及入口/节点本地用户统计继续保留。

两个架构的发行包均包含 `sbctl`、`sbctl-node`、`guard.sh`。从 v0.10.5 升级时，先停止节点 worker 并备份，替换节点 guard，再替换节点工具并启用新版 worker；旧 guard 无法读取格式 2 manifest。主控与节点工具须配套升级，保留现有数据库、模板、探针凭据、配置观察序号、接管日志和账本，不运行 initialize。代理内核不随本次发行升级。

## 验证证据

- [主分支 CI](https://github.com/domoxiaojun/sbctl/actions/runs/44110890826) 成功：Go vet/竞态测试、规定 Node/pnpm 版本下的前端契约、测试、lint、生产构建及工作流语法检查。
- [发行流水线](https://github.com/domoxiaojun/sbctl/actions/runs/44110896621) 的 amd64、arm64、publish 三个 job 全部成功。两种原生 Linux runner 均验证程序版本与标签一致，并完成镜像运行检查；amd64 的 24 项前端单测、45 项浏览器测试通过。
- 下载两份程序包与发行校验文件，SHA-256 校验全部通过；两包内的 guard 与源码一致。
- `release.json` 的版本与提交正确；`manifest.json` 只含上述两种平台，其文件 SHA-256 与固定镜像 digest 一致。

| 程序包 | SHA-256 |
| --- | --- |
| `sbctl-v0.10.6-linux-amd64.tar.gz` | `0e39b75d310bdebb0edad0351731081f6359abd8637aa1b65c8e6ff2ebc72780` |
| `sbctl-v0.10.6-linux-arm64.tar.gz` | `dec33407bf582770c3369fd690bd5d7e5ef392c63f8f09cbf9ecd9c9b97afc9b` |

边界：以上证明发行物构建、CI 验证和发布完成，不代表已部署到生产，也不代表已完成真实 sing-box 配置同步或多节点验收。本轮未读取服务器现状。
