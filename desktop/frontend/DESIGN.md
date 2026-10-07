# Reasonix Desktop — 前端设计系统（写给改 UI 的 agent）

本文件集中沉淀 desktop 前端已存在并已验证的 UI 约定，供 agent 在本仓生成或修改 UI
时先读此文件，而不是临场发明新的视觉规则。

- **违规定性**：违反本文件的改动按**设计系统缺陷**处理，不是风格偏好。
- **收录原则**：只收录树内已存在、有任务出处或检查脚本兜底的约定；本文件不发明新规则。
  每条规则后附出处（任务号 / 检查脚本 / `styles.css` 区段）。
- **与上游 zcode `DESIGN.md` 的关系**：形态仿照（集中式、违规定性），内容按 fork 语境重写。
  两者最大的差异：zcode 走 Tailwind `text-ui-*` 工具类 + `--ui-font-size` 单变量缩放；
  本仓前端是**纯 CSS 变量 token 体系**（`styles.css` `:root`），无 Tailwind，缩放经
  `--font-scale` 乘算 + 五个独立排版区。遇到两者口径不一致时，以本文件为准。
- **改动前自检**：碰任何 CSS 后本地跑 `pnpm check:css`（= `check-css-syntax.mjs` +
  `check-z-index-tokens.mjs` + `check-theme-token-contract.mjs` 三查）。

## ① 字号阶梯

字号唯一来源是 `:root` 的 `--text-*` 阶梯（出处：`src/styles.css` `:root`，token 清单即事实源）：

| Token | 基准值（× `--font-scale`） | 语义别名 |
| --- | --- | --- |
| `--text-2xs` | 10px | — |
| `--text-xs` | 11px | `--font-caption` |
| `--text-sm` | 12px | `--font-code`、`--font-control-small`、`--font-status` |
| `--text-md` | 13px | `--font-control` |
| `--text-base` | 14px | `--font-content` |
| `--text-lg` | 15px | — |
| `--text-xl` | 18px | — |

规则：

- 新 UI 一律消费 `--text-*` 或其语义别名（`--font-content` 等），不新增阶梯外的
  `font-size` 散值。树内既有的 `11.5px`/`12.5px` 类散值是历史遗留，不新增、不要求
  一次性清理（出处：`:root` token 设计 + 树内现状）。
- 全局字号缩放只经设置项 `data-text-size`（small 0.94 / large 1.08 / xlarge 1.18 /
  xxlarge 1.32）改 `--global-font-scale`；**禁止**改 `html` 根字号或手写 px 乘算实现缩放
  （出处：`styles.css` `data-text-size` 段、任务 383 记忆口径同源）。
- 五个独立排版区各有独立的 scale/size/font 注入：interface（body）、conversation
  （`.transcript`）、composer（`.composer-wrap`）、code（`.code/.md-code/.tool`）、
  metadata（`.statusbar/.msg-meta` 等），经 `--typography-*` 自定义属性生效。新 UI 面
  默认继承全局设置，**不得**擅自注册新排版区（出处：`styles.css`「Independent
  typography regions」段及其注释——根 token 在继承前计算，各区根需重新声明比例 token
  才能独立缩放，这是刻意为之的机制而非巧合）。
- 中文字体注意（出处：`:root` 平台栈 + `data-font-family` 预设段）：
  - 默认 `--font-ui` 栈已含 "PingFang SC" / "Microsoft YaHei UI" / "Noto Sans SC"，
    平台特化栈（`data-platform="windows|darwin|linux"`）与用户字体预设
    （yahei/pingfang/noto/custom，mono：cascadia/jetbrains/sfmono/custom）都自带中文回退，
    新 UI 不需要也不应该自带字体族硬编码。
  - `--text-2xs`（10px）对中文笔画密度偏低，只用于极弱元数据；正文与控件文本不低于
    `--text-sm`。
  - 正文行高 `1.6`（body 级），适配中英混排；不要在局部压到 1.2 以下。
- 字重只用既有三档：`--font-weight-regular/medium/strong`（400/500/600）。

## ② 语义色 / token 纪律

对位检查脚本：`desktop/frontend/scripts/check-theme-token-contract.mjs`（`pnpm check:css`
的第三查）。以下「硬规则」由该脚本强制，违反即 CI 红：

- **退役 token 禁用**：`--fg-muted`、`--bg-elev-1`、`--hover`、`--border-strong`、
  `--shadow` 已退役，任何 `var()` 引用直接报错（出处：脚本 `retiredTokens`）。
- **无定义引用禁止**：`var(--xxx)` 引用的 token 必须有定义或带 fallback；React 运行时
  写入的 token 例外（见下）（出处：脚本 undefined-reference 检查）。
- **`:root` 语义别名链固定**：`--stage→var(--bg)`、`--surface→var(--bg-elev)`、
  `--surface-2→var(--bg-elev-2)`、`--surface-3→var(--bg-soft)`、`--panel→var(--bg-elev)`、
  `--border-2→color-mix(...)`、`--text→var(--fg)`、`--text-2→var(--fg-dim)`、
  `--text-3→var(--fg-faint)`、`--shadow-color: #000`、
  `--overlay-surface-bg→var(--bg-elev)`。这组共享语义契约的值不得改写（出处：脚本
  `requiredRootTokens`；`styles.css` 注释「Shared semantic contract. Theme directions
  may override any of these」）。
- **六套主题方向 × 三选择器必须齐**：graphite / aurora / slate / carbon / nocturne /
  amber 每套都要有 dark、forced-light（`[data-theme="light"][data-theme-style=…]`）、
  auto-light（`:not([data-theme])`）三个选择器（出处：脚本 `themeStyles`）。
- **Native Workbench refresh 段不得覆盖具名主题**：`styles.css` 末段
  `:root:not([data-theme-style])`（用户未选方向时的默认调色板）禁止覆盖任何具名方向的
  调色板选择器（出处：脚本 Workbench-refresh 三断言）。
- **amber 锚点值不得漂移**：脚本对 amber dark/light 各 8 个 token 断言精确色值
  （出处：脚本 `amberExpected`）。

用色纪律（出处：任务 525 交付报告 CSS 契约段 + `styles.css` token 分组注释）：

- 新 CSS 全部使用已定义 theme token，**不新增裸值色板**；需要派生色用
  `color-mix(in srgb, <token> N%, <token>)`（525 实测契约：浮卡全套样式零新增裸值）。
- 语义分组即用法：surfaces（`--bg` 页面底 / `--bg-soft` / `--bg-elev` 面板 /
  `--bg-elev-2` / `--sidebar-*`）、文本三级（`--fg` / `--fg-dim` / `--fg-faint`）、
  brand（`--accent` 族，强调/激活/选中态）、status（`--ok` / `--warn` / `--err` /
  `--danger`）、diff（`--add-*` / `--del-*`）、代码不透明岛（`--code-*` + `--hl-*` 语法
  色）、图表固定板（`--chart-1..5` / `--chart-other`——刻意不随主题方向变，保证图表双
  向可读，出处：`:root` chart 注释）。语义色只用语义态，不借 `--err`/`--ok` 做纯装饰。
- 运行时 token 白名单（React/几何代码写入，非主题契约，新 CSS 不消费它们做主题派生）：
  `--composer-height`、`--invocation-color`、`--sidebar-expanded-width`、
  `--transcript-row-estimate`、`--tp-*` 前缀（出处：脚本 `runtimeTokens`/`runtimePrefixes`）。
- **z-index 只用 `--z-*` token**，禁止裸数值（对位脚本：
  `check-z-index-tokens.mjs`）。层级谱系见 ④。
- 主题包机制（用户可安装皮肤）与 token 的关系见 `docs/THEME_PACK.md`：主题包只能换
  语义 token 值，不能跑 CSS；因此新视觉面必须走 token 才能被主题包覆盖。

## ③ 圆角 / 间距层级表

出处均为 `src/styles.css`（`:root` 配方 token + 各组件段实测值）与任务 383 交付报告。

圆角（token 基准 + 组件实测）：

| 层级 | 值 | 出处 |
| --- | --- | --- |
| 全局基准 `--radius` | 9px（默认方向；Workbench refresh 默认调色板下 8px） | `:root` + Native Workbench refresh 段 |
| 按钮 `--button-radius` | 8px（同上 7px） | `:root` |
| 列表行 / 树行 `--list-row-radius` / `--tree-row-radius` | 7px | `:root` 组件配方 |
| 菜单面板 / 菜单项 | 9px / 6px | `.menu` 段 |
| 内容卡片 | 10px | 任务 379 图墙卡 `.experimental-intro__card--wall` |
| 大弹窗 | 12px | 任务 379 `.fork-features-dialog` |
| 大浮层（palette 族，会话墙） | 14px | 任务 505 `.session-wall` |
| 胶囊 / 徽章 | 999px | `.menu` 徽章、379 徽章、`:root` `--pill-*` 族 |

规则：新组件圆角从上表就近取档，不发明新档位；`999px` 只用于刻意胶囊形
（徽章/pill），大小/重要性不构成升档理由（与 zcode 口径同源，按本仓档位落本地）。

密度配方 token（新列表/按钮/树行直接复用，不另起炉灶；出处 `:root` 组件配方段）：

| 配方 | 值 |
| --- | --- |
| `--list-row-height` / `--list-row-gap` / `--list-row-px` | 38px / 10px / 10px |
| `--tree-row-height` | 30px |
| `--button-height` / `--button-small-height` / `--button-icon-size` / `--button-px` | 34px / 30px / 30px / 12px |
| 单列内容最大宽 `--maxw` | 960px |

间距实践阶：4 / 6 / 8 / 10 / 12 / 16 / 24px 为主（各组件段实测主流值）；
flex 内可截断文本加 `min-width: 0`（任务 278 实测教训：长 token 把弹窗行撑破的唯一
残余路径）。布局类缺陷优先怀疑收缩链，不加特判宽度。

三套布局口径与例外白名单（出处：任务 383 交付报告「十条裁决对照表」+ Go 事实源
`normalizeDesktopLayoutStyle`）：

- 布局规范值恰三个：`classic` / `workbench` / `creation`；`workspace` 是退役别名，折叠进
  workbench。**注意区分维度**：`data-platform` 的 windows/darwin/linux 是 OS 平台
  维度，不是布局维度，两者不要混写。
- 十条裁决已闭环：「做」的统一项（#2 分组入口、#5 classic 底栏纯图标 + Tooltip、
  #8 自动化单名、#10 三值注释）已落地并有验收 harness；「不做/缓」的差异
  （#1 classic 无空白项目入口、#3 排序入口位置、#4 header/footer 风格、#6 creation
  feature-zone、#7 creation 折叠按钮、#9 workbench More 死项）是**既定例外白名单**。
  新 UI 工作不得重开这些裁决，也不得把 classic 专属能力顺手「统一」到其他布局。

## ④ 密度与弹窗 / 菜单语言

产品气质（出处：`styles.css` 文件头注释）：单列、开发者密度、终端味；系统字体栈 +
捆绑图标字体（无远程 web-font）；颜色全走 CSS 变量跟随 OS 亮暗。

弹窗宽度族谱（新弹窗先查此表就近取档，不为单一功能发明新档）：

| 档 | 宽度 | 出处 |
| --- | --- | --- |
| 默认弹窗 | 620px | 任务 278 测试钉死的默认调用方 |
| wide 弹窗 | `min(900px, calc(100vw - 32px))` | 任务 278（shell 公式 + 窄屏视口兜底） |
| 特性速览大弹窗 | `min(980px, 94vw)` + `max-height: 88vh` | 任务 379 `.fork-features-dialog` |
| 会话图墙 | `min(960px, 94vw)` + `max-height: 80vh` | 任务 505 `.session-wall` |

图墙 / 卡片墙语言（任务 379 特性速览与任务 505 会话墙同构）：

- 网格用 `display: grid` + `repeat(auto-fill, minmax(236px, 1fr))`，gap 10px
  （379；505 同构 auto-fill）；**禁用**已退役的纵向堆叠列形态（379 明文 retire）。
- 卡片 = 1px `--border-soft` 边框 + 10px 圆角 + color-mix 弱底 + 标题两行截断 +
  hint + 元数据行/徽标（379/505 卡片结构同款）。
- 大墙（上百卡）必须 `content-visibility: auto` 只画屏内卡（505 验收①：200 卡 1ms）。

body portal 浮层契约（出处：任务 525 交付报告——「DOM 存在但永远不可见」事故）：

- portal 到 `document.body` 的浮卡/浮层**必须** `position: fixed`（`html,body,#root`
  100% 高 + body overflow hidden 会把文档流内容裁到视口外；jsdom 无布局引擎测不出
  「渲染了但看不见」，该契约由源码断言测试钉死）。
- z-index 用 `--z-*` token 与同族浮层同层（525：`--z-floating-menu` 与选区工具条同层）。
- 宽高上限视口兜底：`min(420px, 100vw - 16px)` / `min(320px, 100vh - 16px)`，正文
  `overflow-y: auto` 内部滚动。
- 半透明 body portal 类表面需镜像 Windows WebView2 防残影特例（不透明背景、去
  动画/变换；525/`TranscriptSelectionMenu.css` 同款）。

菜单语言（出处：`styles.css` `.menu-backdrop` / `.menu` / `.menu__item` 段）：

- 背板 `position: fixed; inset: 0` + `--z-menu-backdrop`；面板 `min-width: 160px`、
  `padding: 5px`、1px `--border`、9px 圆角、阴影、行间 `gap: 2px`。
- 菜单项 7px 10px 内边距、13px 字号、6px 圆角、hover `--bg-soft`、disabled
  `opacity: 0.4` **保布局**（不塌缩、不换文案）。
- 菜单进出场用既有动画 token（`--dur-base` / `--ease-out`，palette 族另有
  `--motion-pop-scale: 0.98` / `--motion-rise: 4px` 入场形）。

动效纪律（出处：`:root` motion token 注释 + 全局 reduced-motion 塌缩块）：

- 时长四档：`--dur-fast` 120ms（hover/tooltip）/ `--dur-base` 180ms（popover/菜单/
  小入场）/ `--dur-slow` 340ms（抽屉/模态/面板滑入）/ `--dur-slower` 420ms（大浮层
  淡入）；缓动用 `--ease-out` / `--ease-decelerate` / `--ease-standard`，不新造曲线。
- `prefers-reduced-motion: reduce` 有全局塌缩块兜底（animation 压到 0.01ms、
  transition 直接 `none`、`scroll-behavior: auto`），新动画默认无需自带 reduce 覆盖；
  过渡必须用 `none` 而非 0.01ms——near-zero 的 `transition: all` 仍会从旧值出发，
  让同帧几何读数误判并放大 transcript 滚动补偿（出处：塌缩块注释）。
- 性能红线：**禁 backdrop-filter / blur**（Linux WebKitGTK 慢且不一致）；禁远程
  web-font（出处：`styles.css` 文件头多平台注记）。

z 层谱系（`--z-*` token 全集见 `:root`；对位 `check-z-index-tokens.mjs`）。
常用档：局部浮起 2-5 → 布局 resizer 12-13 → inline sticky 20 → 局部 backdrop/popover
30-31 → workspace float 40 → app chrome 70 → drawer 90 → menu 95-96 → dock 100 →
floating-menu 110 → modal 1200 → overlay/toast/tooltip 1300-1302 → onboarding 9999 →
crash 99999。新浮层先在谱系里找同语义档，不插裸值。

transcript 滚动纪律不在本文件重复——见 `desktop/AGENTS.md`「Transcript scroll
discipline」（单写者 / generation fence / 几何事务），碰任何能移动 transcript 视口
的东西前必读。

其他平台注记（出处：`styles.css` 头注释 + 任务 506 段注释）：`--wails-draggable`
标记标题拖拽区，交互子元素必须 `no-drag` 退出；styles.css 末尾追加的实验开关样式段
（如 `data-tab-tier`）须写明「开关关闭时不命中」。

## ⑤ Do / Don't 收口

Do：

- 字号走 `--text-*` / `--font-*` 语义别名；颜色走语义 token + `color-mix` 派生；
  z-index 走 `--z-*`（对位 `check:css` 三查）。
- 新弹窗/浮层先查宽度族谱（620 / 900 / 960 / 980）与 z 谱系就近落档；超视口一律
  `min(Xpx, 94vw)` 或 `calc(100vw - 32px)` 兜底。
- 新列表/按钮复用密度配方 token；新卡片墙用 auto-fill 网格 + 既有卡片结构，
  大墙加 `content-visibility: auto`。
- body portal 浮层带 `position: fixed` + 视口兜底 + 内部滚动（525 契约）。
- 动效走 `--dur-*` / `--ease-*` 四档；新动画依赖全局 reduced-motion 兜底，无需重复写。
- 改完 CSS 本地过 `pnpm check:css`；涉及 bundle 面的改动关注 `check:bundle` 预算
  （棘轮规则与贴顶预警见 `check-bundle-budget.mjs` 与各交付报告，如 505 §5）。
- 尊重 383 三布局裁决白名单与三规范值（classic/workbench/creation）。

Don't：

- 不引入裸值色板 / 一次性色值；不借语义色做装饰。
- 不引用退役 token（`--fg-muted` / `--bg-elev-1` / `--hover` / `--border-strong` /
  `--shadow`）；不在 CSS 消费运行时 token（`--composer-height` / `--tp-*` 等）做主题。
- 不写裸 z-index 数值；不发明新圆角档 / 新弹窗宽度档 / 新字号散值。
- 不改 `html` 根字号或绕过 `--font-scale` 实现缩放；不擅自注册新排版区。
- 不在 Native Workbench refresh 段覆盖具名主题调色板；不动 amber 锚点值。
- 不用 backdrop-filter / blur；不引远程 web-font。
- 不重开 383 已裁决差异；不把 classic 专属能力顺手统一到其他布局。
- 不绕过 transcript 单写者契约（见 `desktop/AGENTS.md`）。

最后重申：以上任一条被违反，定性为**设计系统缺陷**（会进 review/返工），不是口味
差异可以事后商量。本文件未覆盖的视觉决策，先找树内最近似组件的既有做法；确认没有
先例再提新规则，并把新规则连同出处补回本文件。
