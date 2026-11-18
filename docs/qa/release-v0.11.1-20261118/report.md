# sbctl v0.11.1 与 sing-box v1.14.4-sbctl.1 发行回执

## 版本与源码

| 组件 | 版本 | 源码提交 | 发布来源 |
| --- | --- | --- | --- |
| sbctl | `v0.11.1` | `4269b3a696c4a933b6c99aa11f22bb9e4f22e7a9` | [GitHub Release](https://github.com/domoxiaojun/sbctl/releases/tag/v0.11.1) |
| sing-box | `v1.14.4-sbctl.1` | `96e66e71407fa49dc3b44f4f7fcfaa1e3626feb3` | [GHCR 发布流水线](https://github.com/domoxiaojun/sing-box/actions/runs/44310905231) |

sing-box 源码基于上游 `v1.14.4`，提交 `c005f35300ce76427dc285481ec5695f5b179cd2`。

## 发布产物

sbctl GitHub Release 发布于 2026-11-18 13:40:47 UTC。两个镜像均支持 `linux/amd64` 和 `linux/arm64`。匿名读取版本标签、`latest` 时，各组件均返回相同的双架构清单。sbctl 的清单与 GitHub Release 中的 `manifest.json` 一致。

| 组件 | 固定镜像 |
| --- | --- |
| sbctl | `ghcr.io/domoxiaojun/sbctl@sha256:5bfa30869edfdb94547dc5bb7e91cee9c6ed47ceac750b1edb18375efc1d41f0` |
| sing-box | `ghcr.io/domoxiaojun/sing-box@sha256:5882295c7b3e3646f2d7bf84702a066e00614986fe5f627dd6265a9426a179b1` |

两份安装包的 SHA-256 校验通过，均包含对应架构的 `sbctl`、`sbctl-node`，以及与发行源码一致的 `guard.sh`。

| 安装包 | SHA-256 |
| --- | --- |
| `sbctl-v0.11.1-linux-amd64.tar.gz` | `b3b8b9e2a777fcab8c837c3f26e7000300370097565cee358cfaddc6b65a5255` |
| `sbctl-v0.11.1-linux-arm64.tar.gz` | `adbbd337cc527cefd8aa5481e51c0ddb5e923ef4abd3e568f6f5d1de4ae00734` |

## 发布内容

- 管理节点允许零个、一个或多个授权入口。用户订阅按用户生成，入口凭据按稳定 ID 管理。同名或同端口的新入口使用新的 ID。
- 节点工具直接读取 sing-box 的自研 Unix socket。主控通过 Tailscale 上的受限 SSH 拉取累计数据及分页连接记录。连接明细保存已结束连接，保留 90 天，提供域名、目标地址、规则和出站排行。
- 升级涉及主控、节点工具、启动守卫和内核。备份与升级顺序见 [发布及升级流程](../../../ops/controller/release.md#v0111-升级)。

## 构建记录

- sing-box 在 Linux amd64 和 arm64 上通过竞态测试、短连接计数、分页回放及历史缺口测试。两个架构的容器均启动并响应统计快照请求，socket 权限为 `0660`。[内核流水线](https://github.com/domoxiaojun/sing-box/actions/runs/44310905231)
- sbctl 首轮 `v0.11.0` 在 Chromium 回归中有 5 项失败，正式发布步骤跳过。v0.11.1 更新节点接管和入口删除的浏览器用例，将连接明细用例切到正式前端的 HTTP 请求路径，并用模拟 API 验证节点与协议筛选。[首轮流水线](https://github.com/domoxiaojun/sbctl/actions/runs/44312518522)
- sbctl v0.11.1 的 Go 静态检查和竞态测试、前端契约生成检查、单测、lint、生产构建与工作流语法检查通过。[主分支 CI](https://github.com/domoxiaojun/sbctl/actions/runs/44313902222)
- sbctl v0.11.1 两种架构均通过程序版本与发行标签校验、容器运行检查及镜像发布。amd64 通过 24 项前端单测和 46 项 Chromium 回归。[发行流水线](https://github.com/domoxiaojun/sbctl/actions/runs/44313903894)

## 验收范围

编译、镜像构建和浏览器验证均在 GitHub Actions 运行。受管 VPS 升级、真实节点采集及客户端互通仍待独立验收。
