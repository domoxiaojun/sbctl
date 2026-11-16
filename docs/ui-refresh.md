# F0 界面重做与组件来源

日期：2026 年 11 月 14 日。用户要求：查找提供新组件和源码的网站，改善控件和整体设计；排除经典蓝白，保留白色日间与黑色夜间主题，并设计新配色。

## 来源与实际使用

| 网站 | 提供内容 | 本轮使用 |
| --- | --- | --- |
| [shadcn 官方组件与 Blocks](https://ui.shadcn.com/blocks) | 可复制到项目的组件源码及完整后台区块 | 实际引入当前 Base UI / base-nova 的 Sidebar、Select、Tooltip、Avatar、Chart；表单使用现有官方 Checkbox、Dialog、Input、Button |
| [shadcn Registry Directory](https://ui.shadcn.com/docs/directory) | 第三方组件仓库目录和 CLI 入口 | 核对组件来源，作为后续查找入口 |
| [ReUI](https://reui.io/docs) | Base UI 与 Radix 两套组件；组件和示例免费，Blocks 与模板有付费范围 | 阅读高级控件与 Timeline 文档，作为后续复杂组件候选；本轮未导入其代码 |
| [shadcn/studio](https://shadcnstudio.com/docs/getting-started/how-to-use-shadcn-cli) | 组件变体、区块、页面及主题；有免费与付费资源 | 核对源码和安装入口，参考控件组合方向；本轮未导入其代码 |
| [tweakcn](https://tweakcn.com/) | 日夜主题预览、颜色和字体编辑、Tailwind 主题导出 | 参考完整主题变量的组织方式；本项目使用自定义配色，没有声称套用了某个预设 |

对照官方 npm Registry，实际安装的 [React 19.3.1](https://registry.npmjs.org/react/latest)、[shadcn CLI 4.23.0](https://registry.npmjs.org/shadcn/latest)、[Tailwind CSS 4.3.5](https://registry.npmjs.org/tailwindcss/latest) 均与当日 latest 一致。此前外观不足来自组件采用方式、布局和样式覆盖，不能以版本新作为设计质量已合格的证据。

shadcn Chart 的 registry 安装曾选择较旧的 Recharts 3.8.0，已恢复项目的 3.10.1 并通过类型及构建验证。版本锁定继续保留，未为视觉改动升级无关框架。

## 新配色与界面规则

| 语义 | 日间 | 夜间 |
| --- | --- | --- |
| 页面背景 | 暖白 `#f7f6f4` | 石墨黑 `#151413` |
| 内容卡片 | 白色 `#ffffff` | 炭黑 `#1e1c1a` |
| 主文字 | `#292624` | `#eeeae5` |
| 辅助文字 | `#756c65` | `#b4aaa1` |
| 主操作 | 赤陶橙 `#bb4d2d` | 柔橙 `#f69b79` |
| 图表主系列 | `#ba5438` | `#ed9b7e` |
| 图表辅助系列 | 紫灰 `#9681ae` | 淡紫灰 `#ae9ac5` |

颜色、圆角与字体通过 CSS 语义变量统一提供。正文以 14px 为主，辅助文字 12px，标题 26–30px；主要按钮和输入框桌面为 40px，手机主要操作至少 44px。状态同时使用文字、图标和语义色。

总览改为清晰的指标区、简短待办提醒、上传／下载分段图表、节点健康环与地区分布，再展示操作和告警。取消重复说明块和英文装饰标签。日夜主题支持手动切换、刷新恢复及跟随系统变化。

## 验证与截图

已完成 15 项原有 Playwright 场景，覆盖表单、选择、筛选、分页、订阅展示、禁用、操作恢复、生产断开 API，以及 375／768／1440px 的双主题布局。侧栏折叠提示、主题持久化和系统主题跟随的新增场景也通过，合计 16 项；对变更部分定向复跑。基础语义文字色的 14 项对比度计算通过，最低 4.75:1；图表刻度显式采用主题文字色。

新增官方移动断点 hook 调整为 `useSyncExternalStore`，满足项目的 React Hooks 检查。最终类型、生产构建和演示内容排除检查通过；lint 无错误，保留生成组件 Button、Badge、Sidebar 的 3 条 Fast Refresh 导出提示。相关文档 81 处链接、71 个唯一目标全部有效。

| 页面 | 日间 | 夜间 |
| --- | --- | --- |
| 桌面总览 | [1440px](qa/f0-redesign/overview-1440-light.png) | [1440px](qa/f0-redesign/overview-1440-dark.png) |
| 平板总览 | [768px](qa/f0-redesign/overview-768-light.png) | [768px](qa/f0-redesign/overview-768-dark.png) |
| 手机总览 | [375px](qa/f0-redesign/overview-375-light.png) | [375px](qa/f0-redesign/overview-375-dark.png) |
| 手机用户列表 | [375px](qa/f0-redesign/users-375-light.png) | [375px](qa/f0-redesign/users-375-dark.png) |
| 用户表单 | [桌面日间](qa/f0-redesign/dialog-1440-light.png) | [手机夜间](qa/f0-redesign/dialog-375-dark.png) |

截图与测试证明已实现和可操作，不替代用户的审美反馈。仍为 F0 演示原型，没有接入真实后端或 VPS。
