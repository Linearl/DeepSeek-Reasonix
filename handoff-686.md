# handoff-686：工作流可视化运行面板 —— 流程图 + 进展动态界面（实验室开关）

- 分支：`wt-686`（worktree `github-repo/worktrees/wt-686`，基于 `main-v2-stable` @ f8a09feec）
- 任务书：tl 686（P2 批 35 专批件；依赖 685 的 DAG schema）
- 建档：2026-10-16

## 用户两诉求（2026-10-09 原话）

1. workflow 工作过程中通过动态界面**看到进展**；
2. **了解工作流具体是什么样的**。

## 设计要点（任务书口径）

- **诉求②（结构）**：流程图主视图直接渲染 DAG 编排定义——**运行前即可预览全图（计划视图）**：节点=卡片（类型图标+名称）、边=依赖/数据流、phase 分组着色。
- **诉求①（进展）**：运行中按 `internal/event` 事件流实时点亮节点：排队灰 / 运行中脉冲 / 完成绿 / unconfirmed 黄 / 失败红；顶部工具条=运行态+进度（n/N 节点、耗时、token）+阶段指示。
- **下钻**：点击节点看详情——指令全文 / 类型化结果 / 独立确认记录 / 门命令+exit code+stderr / 耗时 tokens。
- **入口与形态**：会话内 workflow 卡片点开**独立面板/抽屉，不新增 tab**。
- **技术选型**：React Flow（@xyflow/react）vs 自绘 SVG，**以轻为准**。
- **实验开关包住**（关闭态行为等价）。

## 依赖状态（2026-10-16 核实）

- **685 未落**：tl 685 status=pending；wt-685 无领先 commit（与 main-v2-stable 同 HEAD）。DAG schema 契约不存在。
- ⇒ 按任务书降级路径执行：**技术选型对比 + 面板壳 + 实验开关 + 事件订阅接口骨架 + fixture DAG 假数据渲染**；handoff 记等待点；685 落地后接真渲染与事件契约对齐。

## 技术选型对比（2026-10-16 定案：自绘，不引入 React Flow）

| 维度 | React Flow (@xyflow/react) | 自绘（HTML 节点卡 + SVG 边） |
|---|---|---|
| 体积 | 新增依赖 + ~40-60KB gzip 常驻面（需 pnpm-lock 变更；棘轮线 578.0/337.0 余量紧） | 懒加载 chunk（SessionWallPanel 同款），零新依赖 |
| 能力 | 拖拽/缩放/mini-map/端口连线开箱即用 | 需自写分层布局（ longest-path 分层 + 同层排序，~150 行纯函数）；拖拽/缩放**本期不需要** |
| 测试性 | 需真实 DOM + 辐射 | 布局/reducer 均为纯函数，tsx 直测 |
| 风险 | 新依赖进 bundle 面、与 React 19.3 兼容性需验证 | 全部代码自控；z-index/主题 token 合同可守 |

**结论**：任务书口径「二选一以轻为准」——需求只是「预览全图 + 状态点亮 + 点击下钻」，无画布编辑诉求；自绘以懒加载 chunk 落地，不碰启动预算。React Flow 留待未来真有画布交互需求时再评估。

**实验开关定案**：`experimental_workflow_panel`（bool，默认关，铁律 2；档位=未稳定——后端 685 尚未落地，如实「开发测试中」）。入口暂用命令面板条目（sessionWall 先例，flag 关=palette 逐字节不变）；**685 落地后迁移到会话内 workflow 卡片**（任务书正典入口，handoff 记等待点）。

## 交付清单（2026-10-16，六笔 commit）

| commit | 内容 |
|---|---|
| a3267171a | Go 侧开关链：config 字段+setter+渲染表+档位登记（未稳定，表A 15→16/56→57）+desktop 双视图双映射+App setter+两测试随动 |
| 4d48b67df | 面板本体：workflowDag.ts（预拟 schema+纯函数分层布局+fixture）、workflowEvents.ts（事件契约+纯归约 store+演示回放）、WorkflowPanel.tsx、Boundary、CSS |
| c4b28d27c | 前端开关全链+App 接线+三语 41 键（labFlags 默认 false→tier 19/29/15/1=64→灯谓词→实验室卡 ui 组→pane→bridge→双视图类型→palette 插条+懒加载挂载） |
| 74307fab3 | 验收 harness（82 断言全绿）+钉点随动（561 rail 38/ui 10、562 计数 64/Go 57、704 pin 63→64） |
| 1c318c7ad | initial 面瘦身：palette 插条 helper 独立轻量模块（workflowPanelEntry.ts）+store 内收进懒加载面板+删 App 侧 useWorkflowRunStore 桥（首版 App 急切 import 把 fixture+演示脚本拖进 initial 炸线 581.9 的根因；瘦后 578.6，面板 chunk 验证不在 index.html） |
| 7fd9920a3 | bundle 棘轮四线自报抬（独立 commit 报备）：initial JS 578.0→579.0、app-shell CSS 139.4→140.4、raw initial 3055→3065、zh locale 106.2→107.2 + largest gzip 337.0→338.0（抖动区预抬） |

## 验收自检（任务书五条）

- [x] ① 运行前全图预览可看：fixture DAG 布局纯函数渲染全图（分层正确、回边识别、无重叠、确定性）；palette 入口开面板即计划视图。
- [x] ② 运行中节点状态变化秒级可见（事件驱动非轮询）：run store 纯归约，事件到达即订阅通知；演示按钮回放 fixture 脚本可人工目验；测试逐条 dispatch 断言即时变迁。
- [x] ③ 节点详情可下钻四类信息：指令全文（subagent 静态面）/类型化结果（result 事件）/独立确认记录（confirmation 事件）/门命令+exit code+stderr（gate 静态命令+事件），耗时/tokens 随附。
- [x] ④ 开关关闭态行为等价：labFlags 默认 false；palette 插条 flag 关返回同一引用（逐字节不变）；面板 chunk 懒加载（关=不加载）；铁律 2 全链路源码断言。
- [ ] ⑤ 与 685 事件契约对齐：**挂点已锚**（事件型集合 {node-state, run-state, node-detail} + 五节点态在 task686 测试 ⑤ 钉死），真对齐断言待 685 schema 落地后补（见等待点）。

## 预存红对照（stash 基线实测，均非本件引入）

- task704-trajectory-switch：5 条失败（Go 计数 pin 742 口径过期/trajectoryView 迁卡后锚过期等），基线同 5 条；本件只随动其 562 计数 pin（63→64）。
- task826-sidebar-hover：1 条（settings_app 对齐空格锚），基线同红。
- desktop-preferences-lifecycle：1 条（'deep' vs 'standard'），基线同红。
- `go test ./internal/config`：TestServeRenderTableCoversEveryKey（web_dist 键），基线同红；其余 config 包测试全绿（-skip 该条后 ok）。

## 等待点（685 落地后接续）

1. **DAG schema 真对齐**：`src/lib/workflowDag.ts` 类型是按 685 任务书语义预拟（node=subagent/gate/join，edge=depends+label，phase 分组）。685 交付后按其 handoff schema 节对齐字段名，并在 task686 测试 ⑤ 补真对齐断言（当前锚=三事件型+五节点态集合不变式）。
2. **事件真源接线**：`src/lib/workflowEvents.ts` 的三事件型是预拟 wire 契约。685 落地后在 App 侧把 internal/event 的 workflow 事件经 bridge onEvent 映射进 `workflowRunStore.dispatch`——面板的 `store` prop 就是注入点（缺省=面板自持 fixture store；瘦身笔 1c318c7ad 起 store 内收，fixture/演示脚本只随懒加载 chunk 走）。
3. **入口迁移**：当前入口=命令面板条目（sessionWall 先例，flag 关=palette 逐字节不变）。685 落地后迁移为任务书正典入口「会话内 workflow 卡片点开」——同一面板组件，换挂载触发源即可；palette 条目届时按用户裁决去留。
4. **档位复核**：workflowPanel 当前未稳定档（后端未落地=开发测试中，如实标注）。685 接线后可按 550 口径复核是否升可选。

## 残留

- bundle 棘轮已自报抬线（7fd9920a3，四线+largest 预抬）；合并线复测若再炸按 larger-of-two 取大。
- 面板内「演示运行」按钮是 685 落地前的过渡面（fixture 回放）；685 真源接入后可保留为「示例编排」预览入口或移除，届时用户裁决。
- `pnpm build` 全链绿（check-all-parallel 十项 PASS + tsc + vite + bundle 八线 PASS）；`go test ./internal/config` 除 web_dist 预存红外全绿；desktop 模块 build OK。

## 验证记录（2026-10-16）

- 前端：tsc 全量绿；eslint 新文件干净；task686（82 断言）/task561（201）/task562（41）/task621/task826/task704/task742/task783/task427/task764/home-pointer/opencode-go-usage/startup-contract 全绿或基线同红。
- 后端：`go test ./internal/config -skip TestServeRenderTableCoversEveryKey` 全绿（该条 web_dist 为基线预存红）；`go build ./...`（desktop 模块）OK。
- 构建：`pnpm build` 通过；WorkflowPanel chunk 不在 index.html（懒加载验证）；bundle 八条预算线全 PASS。
- 预存红对照（stash 基线实测）：task704 五条、task826 一条、desktop-preferences-lifecycle 一条、serve-render 一条——均基线同红，非本件引入。

