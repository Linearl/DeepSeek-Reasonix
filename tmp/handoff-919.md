# handoff-919 —— 真机体验门 FAIL 治理（P0 批 34 主件）

- worktree：`github-repo/worktrees/wt-919`（main-v2-stable @ f8a09feec）
- 数据源：414 bench 周趋势第 1 期基线（`docs/report/ebdd周趋势/ebdd周趋势-20261015.md`，证据 `tmp/414-weekly-20261015.json`）
- 状态：进行中（boot 门修复已落地）

## 四门读数（基线）

| 门 | 读数 | 阈值 | 判定 |
|---|---|---|---|
| boot 热路径 P95 | 45011ms（n=160，P50=6429ms） | < 1000ms | FAIL |
| 打开历史对话→可发消息 P95 | 4021ms（n=85，P50=963ms） | < 2000ms | FAIL |
| 切 tab render P95 | 170ms（n=674，P50=51ms） | < 150ms | FAIL |
| 发消息→首响 P95 | 129509ms（n=33，P50=16561ms） | ≤ 60s | FAIL |

## 一、四门归因（2026-10-16 实测日志 desktop.log* 全量 + 真机扫描计时）

### ① boot 46.9s：三层真相归因

**样本**：当前 168h 窗口 80 次 boot，热路径（assembly hit 配对）54 次。

1. **第一层：稳态达标**——白天（11-13 时）热路径 boot total 3.2-6.5s，非长尾。任务书「稳态 941ms」对应更好时段；但**热路径 P50 本身已 17.3s**（本窗口），超标主体是第二层。
2. **第二层：skills 扫描相是头号热点**——`boot: agent:tools stage` 分解 80 次：`skills_ms` P50=6157ms P95=53731ms max=86044ms，占 agent:tools 主体（prep/cmds/capability/executor 均 <1s）。深夜（23-02 时）skills 相 36-86s。
   - **根因**：`skill.Store.discoveredSkills()` **零缓存**——每次 `List()`/`SlashList()`/`Read()` 全量重扫磁盘 217 个技能目录（每个读 SKILL.md 全文 36KB + references/*.md 拼接 + scripts 列举）。实测一次全量扫：**冷 742ms / 热 372ms**。
   - boot 冷路径一个 build 调 List() **两次**（skillStore + allSkillStore 两个 store 实例各扫一遍）；SlashList 再扫；**运行期每个 capability 请求经 catalogFn 闭包再调 `skillStore.List()`**（boot.go:2081/2114）——请求期反复全扫。
   - 深夜 36-86s：磁盘争用窗口（OneDrive/杀软同步时段）叠加全量读，放大 10-86×。
3. **第三层：config/controller/prompt 相长尾**（>10s 队列 config p95=27.9s、controller p95=22s、prompt p95=18.3s）——疑为同窗口系统性 IO/CPU 争用（推演：与 skills 同时段爆发、同 boot 内各相齐涨），非独立代码热点。

**修复（已落地）**：`skill.Store` 加 per-file 解析缓存 `ParseCache`（键=path，验证=mtime+size+stem/scope/requireMarker+references/scripts 边车签名；命中重放 missing-description warning；transient IO 错误不缓存）；boot 冷路径两 store 共享一个 ParseCache。
- 实测（217 真实技能）：warm List **372ms→120ms（3.1×）**、SlashList **330ms→129ms**、Read **279ms→110ms**；boot 双 store 第二扫 **451ms→226ms**。
- 新增 7 条契约测试（外部编辑/存储变更/边车编辑/警告重放/共享隔离/并发/transient 不缓存）全绿。

### ② open-topic 4.4s

- 合成口径当前窗 P95=1600ms（PASS，n=6 小样本）；**慢开 census FAIL n=17 P50=3806ms**——17 条全部是**同一个 48MB 巨型会话**（topic_20261005-121908 collab.jsonl）的反复打开。
- 分相归因（open session slow 日志原文）：`resolve` 673-8380ms + `tabLock` 0-3366ms + `rest` 104-2028ms。resolve=`continuePathForOpen`（catalog 查询，巨型会话下 catalog ListTopics 全量遍历）+ tabLock=等 a.mu（大文件保存/回放占锁）。
- **归因**：单一超大会话（48MB jsonl + 62MB events 超限 WARN 同源）反复打开拖垮 census；非普遍路径问题。

### ③ render 174ms（切 tab）

- 当前窗 render P95=69ms **已 PASS**（n=102，861 readSnapshot 缓存生效）；周报窗口（含 10-12~10-14 旧构建日志）P95=170ms FAIL。
- 余热：WARN slow switch stage 34 条（switch-tab:history/bridge 1331ms、ancillary effort 1566ms、open-topic:e2e 1235ms）——hydration/effort 侧，非 render 计时口径本身。
- 861 已定位未修的 #49 稳态 commit 链（Transcript.tsx:178 每 token reconcile + MarkdownHistory geometryChanged 回写环）为**稳态流式渲染**问题，不进切 tab render 门读数。

### ④ 首响 145s

- 当前窗 P95=83.2s 仍 FAIL（n=32）；>60s 慢 turn 3 个，全部特征=**巨型 prompt**：promptTokens 641K/279K/754K/437K/727K/866K。
- 84s 那拍时间线（turn ledger 实证）：queued→started 12.7s（排队）→ stream_attempt 24.5s → 首个 tool_dispatch 84.5s=**单次 prefill 641K tokens 耗 60s**（cache miss 全量前缀）。
- **归因**：慢 turn 主因=巨型 transcript 的 prefill TTFT（与 handoff-912「1.82M transcript 反复全量」同域，912 修的是压缩失败重试/永不全量，未改变「压缩成功后仍 400-900K」的存量大投影）。窗口内 13 个 912 后新 turn：6-16s 为主，>60s 仅 1 个（84s，同上巨型 prompt）。

## 二、进度

- [x] worktree + 计划 commit（55692dd53）
- [x] 四门归因（本文件第一节，日志实证）
- [x] boot 门修复：ParseCache + boot 双 store 共享 + 7 契约测试
- [ ] open-topic / render / 首响：归因已给，判断是否需代码治理还是达标路径
- [ ] bench 复测
