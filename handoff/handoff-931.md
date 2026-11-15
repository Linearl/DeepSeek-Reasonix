# handoff-931 —— transcript 滚动回弹：流式更新期间陈旧锚写回（753 首对齐豁免面收口）wt-931

- 日期：2026-10-16/17
- worktree：`github-repo/worktrees/wt-931`（分支 `fix/931-transcript-scroll-rebound`，基 `main-v2-stable` tip `301ede029`，前任零 commit 零 dirty）
- 任务书：任务 931（P2，UI与交互；现场 2026-10-16 16:54 卡顿分析会话，截图 clipboard-20261016-165424）
- 纪律：切片 commit、显式路径 add、禁 stash/merge/rebase、status 净；中文 message「931 」开头；源码改动走 Edit/Write；前端改动禁 wails generate

## 一、勘察结论（根因机理）

### 1.1 滚动状态机速览（transcriptKernel.ts）

- `intentValue`：`tail`（跟底）/ `reader`（阅读）；`anchorValue`：`{kind:"tail"}` 或 `{kind:"block", blockKey, offsetPx}`（视口顶块 + 块内偏移）。
- 流式期间 reader 态的修正链：`TranscriptWindow` 测量发布 → `onGeometryWillChange` → `beginAnchorRestore`（useTranscriptKernel.ts:353）→ `kernel.begin("restore", kernel.anchor)` → 几何提交 → `correctAnchor`（transcriptKernel.ts:369）按 `anchor.top + offsetPx` 写 scrollTop。
- 任务 753 锚漂移护栏（transcriptKernel.ts:398-411）：**仅当 `alignedAnchor` 非空且 key 与锚块相同**（=「同锚第二次对齐」）且目标拉离当前 scrollTop 超 `max(320px, 视口高)` 时拒绝写回、就地重锚（outcome=anchor-drift）。**key 不匹配或 alignedAnchor 为空（即「首对齐」）一律豁免放行**。

### 1.2 洞：锚可以「未经对齐」而变陈旧，首对齐豁免便成了传送门

753 豁免首对齐的前提假设是「首对齐的锚必然新鲜（刚 capture / 显式 jump / 会话恢复）」。实测有两条路径能造出**陈旧的块锚**，且都落在首对齐豁免面上：

| # | 陈旧源 | 机理 | 证据 |
|---|---|---|---|
| ① | **deferredStructural 手势结束恢复用「冻结旧锚」** | 手势期间到达的结构性事务（restore/prepend/display-change/composer-resize）被 defer，锚冻结在 **defer 时刻**（transcriptKernel.ts:322-327）；用户在手势内继续滚动（observeNativeScroll 已把 `anchorValue` 更新到新位置）；手势结束 `endUserGesture` 用 `deferred.anchor` 恢复（:287），`begin()` 把 `anchorValue` **回拨成旧锚**（:341）→ 下一次几何提交把视口写回旧位置。旧锚 key 与 alignedAnchor 多半不匹配（key 错位）→ 首对齐豁免 → 无视 753 护栏直接写回 | transcriptKernel.ts:287,322-327,341；transcript-kernel-races.test.ts:144-151（defer 语义现测钉的是「用户没继续滚」的窄例） |
| ② | **原生动量越过手势租约后位移不再被观察** | 租约 = 最后一次 wheel 后 320ms（NATIVE_GESTURE_IDLE_MS）；租约过期 `endUserGesture` 后，原生动量仍在发 scroll 事件，但 `observeNativeScroll` 对无手势事件直接 return false（:248）→ 视口继续移动而锚不动；下一次流式几何提交 `beginAnchorRestore` 把陈旧锚当首对齐写回 → 「回弹」。若最后 capture 恰在上一条用户消息处（上滑回读再快速下滑的常见路径），回弹目标正是「上一条用户消息附近」——与现场截图吻合 | transcriptKernel.ts:240-255（!userGesture 早退）；useTranscriptKernel.ts:21（320ms）；:413-426（onScroll 只在租约仍活时续租） |

两条腿的共同点：**锚陈旧但「从未对齐过该 key」→ 753 护栏按 key 判豁免 → 首对齐直接放行写回**。753 报告自己声明的豁免面（「首次对齐（新 capture/显式 jump/会话恢复）豁免」）被陈旧锚穿透。

### 1.3 排除项（任务书方向④确认）

- 与 928 canonical 风暴/hydrate **无关**：本件纯前端滚动内核状态机，无后端事件卷入；detachSurface/replaceSurface 在流式中不触发（sessionKey 不变，useTranscriptKernel.ts:264-274）。
- surfaceKey（`geometrySessionKey:revealSignal`）流式期间稳定，任务书怀疑的「流式中 surface 重建清 alignedAnchor」不是主触发面（tab 切换回来的 restore 属 AC8 合法语义，保留）。
- desktop.log 16:39-17:09 窗口无 blank sentinel / surface 重建 / session-gen 事件（grep 已查）。

## 二、修法（最小修）

### ① deferredStructural 恢复改用「当前锚」（治腿①）

`endUserGesture`：`this.begin(deferred.kind, deferred.anchor)` → `this.begin(deferred.kind)`（默认参数 = 当前 `anchorValue`）。defer 锚的唯一合法取值就是 defer 时刻的 `kernel.anchor`（全部调用方传的都是它），用户手势内若有真实位移，当前锚必然更新更晚、更新鲜；用户没滚时两者相同，语义不变。`DeferredStructuralTransaction.anchor` 字段随之删除。

### ② 首对齐豁免收口为「surface 恢复豁免」（治腿②，收 753 豁免面）

`correctAnchor` 的漂移护栏从「同 key 二次对齐才查」扩为「**所有被动修正（restore/display-change/composer-resize）的首对齐也查**」，豁免面精确收窄到三类：

- 显式跳转 `kind === "jump"`（用户意图，必须写）；
- prepend（语义独立——窗口补偿走 `writeStructuralOffset` 直写，此处保持 753 原状不扩大）；
- **surface 恢复**：`replaceSurface` 恢复出块锚时记 `surfaceRestoreAnchorKey`，该 key 的第一次修正豁免（AC8 会话恢复语义保留）；用户真实位移（observeNativeScroll）或该豁免被消费即清除。

新拒绝对 outcome 记 **`anchor-stale`**（与 753 的 `anchor-drift` 区分，供现场诊断面板判别「护栏触发形态」），拒绝分支复用 753 的就地重锚逻辑（保像素、intent 不降级、重锚当前视口顶块）。

### 兼容性论证

| 场景 | 旧行为 | 新行为 |
|---|---|---|
| fresh capture 后首对齐（目标≈当前位） | 写（豁免） | 写（位移小，护栏不触发）——不变 |
| 753 AC8：surface 恢复大偏差 | 写 | 写（surfaceRestoreAnchorKey 豁免）——不变 |
| 显式 jump 大偏差 | 写 | 写（kind 豁免）——不变 |
| prepend（753 未扩大，维持原状） | 仅同 key 护栏 | 同左——不变 |
| 陈旧锚首对齐大拉离（本 bug） | **写（回弹）** | **拒 + 就地重锚（anchor-stale）**——修复 |
| 同 key 二次对齐大漂移（753 AC7） | 拒（anchor-drift） | 拒（anchor-drift）——不变 |
| 无 viewport 上下文 | 逐字节不变 | 逐字节不变 |

已知取舍：fresh capture 后、首对齐前的亚秒窗口内若发生超一屏的上方塌缩，旧代码会跟随锚块（钉住内容），新代码按 753 对 aligned 锚的既有决策「保像素、就地重锚」。窗口极窄且与 753 决策一致，接受。

## 三、验收（单测钉住）

- AC1 腿①：手势内位移后 defer 恢复不回拨锚——`endUserGesture` 后 `kernel.anchor` 为手势末位置；correctAnchor 不写回 defer 时刻位置。
- AC2 腿②：陈旧锚首对齐（alignedAnchor key 错位或 null）+ 超一屏拉离 → 拒绝、零写回、outcome=`anchor-stale`、就地重锚当前视口顶块、intent 保持 reader。
- AC3 保留①：surface 恢复（replaceSurface 恢复块锚）大偏差首对齐仍写（AC8 语义）。
- AC4 保留②：显式 jump 大偏差仍写；小位移首对齐（fresh capture）仍写。
- AC5 回归：753 全套（task753-scroll-anchor.test.ts）+ transcript-kernel / kernel-races / task267 / geometry-commit 全绿；check-single-scroll-writer OK。
- 源码钉：settleGeometry 仍向 correctAnchor 传 viewport（753 防回退钉沿用）。

## 四、执行记录（2026-10-17 完成）

### Commit 列表

| commit | 内容 |
|---|---|
| `4b5e57ab4` | 诊断 handoff 落盘（根因机理 + 两腿修法 + compat 论证 + AC 矩阵） |
| `cefb4907e` | 修法落地：kernel 两腿 + surfaceRestoreKey 保 AC8 + task931 单测 24 项 |

### 修法实现细节（相对 handoff 二节的落地差异）

- 豁免判定落点：`correctAnchor` 内 `sameAnchorAligned || passiveFirstAlignment`，其中 `passiveFirstAlignment = active.passive && kind !== jump && kind !== prepend && !surfaceRestoreFirst`。豁免三类 = 显式 jump（kind 天然非 passive）、prepend（kind 排除）、surface 恢复（`surfaceRestoreKey` 一次性键匹配，真实位移 observeNativeScroll 即清）。
- `ActiveTransaction.passive` 随 `begin(kind, anchor, {passive})` 传入；deferredStructural 记录 passive 并在 endUserGesture 恢复时透传（非硬编码）。
- 新拒绝对 outcome=`anchor-stale`；同 key 二次对齐拒绝保持 `anchor-drift`（753 观测语义不变，现场诊断面板可区分两种触发形态）。
- 无 viewport 上下文时闸门不启用——`jumpToBlock` 直调路径与 753 兼容语义逐字节不变。

### 验收证据

- AC1 ✅ defer 恢复用当前锚：手势内位移后 resume 写 900（实际位置）而非 500（手势起点）。
- AC2 ✅ 陈旧被动首对齐拒绝：零写回、outcome=anchor-stale、就地重锚 step-9、intent 保持 reader。
- AC3 ✅ AC8 角例：surface 恢复锚的首个被动修正仍写（一次性豁免）；第二次同键远距被动修正被闸（豁免不复用）。
- AC4 ✅ 显式 jump 大偏差仍写；小位移被动首对齐仍写。
- 753 回归 ✅ 同 key 大漂移拒绝且 outcome 仍 anchor-drift。
- 兼容 ✅ 无 viewport 逐字节不变；task931 共 24 项全绿。
- 套件回归 ✅ task753 21 / transcript-kernel 43 / kernel-races 29 / task267 19 / geometry-commit / blank-sentinel 24 / geometry-read-cache / find 48 / materialization / command / fold-preference 10 / question-jump 26 / nested-scroll-handoff 21 / creation-scrollbar 11 / selection-retention 33。
- tsc 全量 `--noEmit` 绿；check-single-scroll-writer OK。
- 预存红申报：`transcript-viewport.test.tsx`「jump-to-bottom visibility after reader takeover」jsdom 超时——**基线 301ede029 同红**（临时 detached worktree 复测确认，非本支引入；与 753 报告所记预存红同一条）。

### 残留

- AC5 实机验收待用户：流式中上翻停手后新增量到达，视图不再回弹到上一条用户消息（jsdom 无法断言真实 WebView 动量/合成滚动，行为级只能钉 kernel 状态机）。
- 现场诊断面板读法：回弹若仍复现，抓 kernel 事件流——`outcome=anchor-stale` 出现即本防线触发（陈旧锚被拒）；若 teleport 且无 anchor-stale/anchor-drift，说明走了无 viewport 上下文路径，需再报。
- 前端测试环境：worktree 内 node_modules/wailsjs 为指向主仓的 Junction（用完随 worktree 删除）；未跑 desktop 全量 go test（纯前端件）；未 wails generate。
