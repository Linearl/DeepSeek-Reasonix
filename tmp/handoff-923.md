# 任务 923 交接（wt-923）—— 锁内 IO 剥离专项：Session.save 锁内全量重放/写盘 移出或批写

生成：2026-10-16 | 分支 wt-923（基 main-v2-stable tip f8a09feec，已含 885 锁有界化 / 884 marshal 出锁 / 889 监测）| 状态：**已完成**（两切片落地+全量回归，待用户指定会话合并）

## 一句话结论

save 锁内两个 O(全量) CPU 大头出锁：**diff 全前缀双份 marshal 加 DeepEqual 快路径（实测 68.5ms→5.3ms，12.8 倍）**、**transcript digest 增量化（全量 47.2ms→增量读 635ns）**——4000msg 合成会话每次版本变化的 save 锁内 CPU 从 ~116ms 降到 ~6ms；两者全部 fail-open 回退、摘要/分类字节语义 bit 级不变。checkpoint 全量重写（187ms）与 display index 全量重建（323ms）仍留锁内——语义改动大（stale-window/代序论证），已给量级表留另立项。

## Commit 列表

1. `0f79020eb` handoff：同域件读数对齐 + 锁内段落地图 + 五候选量级利弊取舍
2. `e41d95549` 切片C：messagesEqualForStorage DeepEqual 快路径 + 对照实现穷举等价测试 4 个 + 分段量级 benchmark
3. `c559a8bd1` 切片B：增量 transcript digest（Add/AddBatch 漏斗喂流、save 三重一致即取、失配回退全量+重播种）+ 12 个正确性测试

## 验收对照

| 验收 | 结论 | 证据 |
|---|---|---|
| ① 并发写读数下降 | ✅（实测段级） | 4000msg 锁内 CPU：diff 68.5→5.3ms（12.8x）、digest 47.2ms→635ns；`BenchmarkT923LockSegment*` 可复跑；885 的 `save phases locked_total_ms` 观测点在装机后可直接对比（旧口径 023554 会话 4482~8321ms 中 digest+diff 约占 100~150ms/次——是「每周期都付」的常项；重 IO 的 rewrite/index 段仍留锁内，见残留） |
| ② 885/196 观测点不回归 | ✅ | 885 四测试（同键排队链/有界失败/跨会话不互等/双会话不拖累）+ 710 成本形状（append<full/2、redundant≈0）+ DAG stale-prefix 全绿；`save phases`/`dag state for save` 打点未动 |
| ③ 老会话数据兼容（往返） | ✅ | TestT923IncrementalDigestLongMixedWorkload 末段：30 轮混合负载（append/rewrite/checkpoint 交错）后 Save→LoadSession，往返 digest 相等；增量摘要与全量重算 bit 级一致断言贯穿全部测试 |
| ④ 与 885/884/889 既有测试全绿 | ✅（1 预存红申报） | `internal/agent` 全量 628s：仅 `TestInterruptedRecoveryKeepsRedactedFailureSummary` 红——**基线 f8a09feec 临时 worktree 单跑同样红**（已实证后删），预存红与 923 无交集；`internal/control` 全量 ok（495s）；根模块 `go build ./...` + desktop 模块 `go build ./...` 均过 |

## 锁内段落量级表（4000msg 合成会话，实测；外推 6434msg 现场按线性标注）

| 段 | 每次版本变化 save 的锁内成本 | 923 后 |
|---|---|---|
| DAG diff（全前缀分类） | 68.5ms | **5.3ms**（DeepEqual 快路径） |
| transcript digest | 51.5ms（47.2 复跑） | **635ns**（增量流）；首存/rewrite 后首次仍付全量并重播种 |
| checkpoint 全量重写（非 pureAppend） | 187ms | 未动（D 候选，另立项） |
| display index 全量重建（非 append） | 323ms | 未动（E 候选，另立项） |
| dagStateForSave 重放 | 命中 graph cache 时 O(新增)（196 已治） | 未动 |

## 残留 + 风险面

1. **D（checkpoint 后台批写）/ E（display index 出锁）未做**：单次成本最高（187/323ms）但语义改动大——.jsonl 从「锁内与 log 同代」变「滞后代」需 stale-window 观测先行；display index 移锁外需逐写者代序论证。量级与利弊已录候选节，建议另立项、先补「read model 与 log 代差现网频率」观测。
2. **增量 digest 正确性契约**：依赖「所有改序列化字节的变更点必 bump version/rewriteVersion」。923 已审计全部原地修改点（session.go/protocol_recovery/tool_recovery/write_recovery/tool_result_checkpoint/save_dag ID 回写）；**未来新增原地内容修改若漏 bump，会静默错摘要**——契约已写进 save_digest_incremental_923.go 文件头与 Session 字段注释；漏网场景被 count/version/rewriteVersion 三重 guard 拦截为「回退全量」而非错值（guard 只有在变更点完全绕过 version 体系时才会失效）。
3. Add 内 `expireProtocolRecoveryLocked` 是 rewrite 形变更（bump rewriteVersion），在捕获 pre 值之前执行——捕获点特意放在其后，expire 触发时流会正确失效（测试 AddDecisionReceipt/ConsumeFinalReadinessRecovery 分支覆盖）。
4. 首存、Rewrite 后首次 save、跨进程恢复后的首次 save 仍付一次全量 digest（并重播种）——预期行为，非常态成本。
5. schema-1 的 classify 全量 loadSessionUnlocked（885 申报残留）保持锁内原位：只在 drift/无 authority 触发（writer-bound 快路径覆盖日常），挪锁外有 TOCTOU 风险，收益/风险比不划算——已在候选 A 节论证。
6. 本机 -race 不可用（无 gcc，CGO 需 C 编译器，889 已申报同环境限制）；增量流全部状态在 s.mu 下读写。

## 中断恢复 / 复跑

```bash
cd github-repo/worktrees/wt-923
go test ./internal/agent/ -run "TestT923|TestCrossSessionSavesDoNotShareLockKey|TestSameSessionSaveWaitChainBounded|TestBlockingSavePathWaitBounded|TestConcurrentSessionSavesBothBounded|TestSyntheticLargeSessionSaveCostShape" -count=1
go test ./internal/agent/ -run XXX -bench BenchmarkT923LockSegment -benchtime 3x   # 量级表复跑
go test ./internal/agent/ ./internal/control/ -count=1 -timeout 20m               # 包级（1 预存红见上）
```

## 同域既有件读数对齐（885 / 196 / 889，勿重复）

| 件 | 已交付 | 关键读数/结论 | 与 923 的边界 |
|---|---|---|---|
| **196** | dagStateForSave 全量重放打点（extended/reason 布尔）+ graph cache；`LoadSessionTail` hydrate 窗口 | 467MiB 全量 replay=19.49s；728MB events 会话锁内全量重放=8~30s 等锁真因；`save end ms>15s` 24/179 条 | 196 治了「重放」的缓存；**digest/diff/rewrite/display index 仍锁内全量**，是 923 主攻面 |
| **885** | 阻塞 save 路径锁等待有界化（sessionSavePathLockWait=30s → ErrSessionFileLockHeld 走既有降级）+ MkdirAll 出锁；4 个并发锁测试 | 锁键 per-session 不共享（并发实测）；同键排队链：等待=前面所有临界区之和；023554 会话 locked_total_ms 4482~8321ms（238MB events / 6434 msgs）；**残留申报：classify 全量 loadSessionUnlocked、display index 重建仍在锁内——本件接续** | 885 只治「等多久」；923 治「持锁者在干嘛」 |
| **889** | App.mu 持锁段路径解析 convoy 的**监测**（reconcile/snapshot 分段打点 + effort-timeout-report 工具） | App.mu 锁面与 save 锁面是两把锁互不混同；修复另立项（正交） | 889 修复不在 923 面内 |
| **884** | schema-2 旋转门 live 估算 Σ整链→union 去重；size gate 全量 digest→sessionMessagesEncodedSize（只 marshal 不 hash） | 旋转恢复运转 | 884 已把 size gate 的 digest 移出；**snapshot digest 本身仍全量** |
| **580** | — | lock wait 7997/9840ms 实测同域（保存链拥堵） | 与 196 同因：持锁段重 |

## 现场读数（2026-10-16 desktop.log，当前包）

- 常规小会话：locked_total_ms 1~18ms，noop 快路径（save-verified / clean-load）命中良好。
- 中型会话（cooking_action_recognition deepseek）：locked_total_ms=1075（noop=""，即走了完整分类+写盘链）。
- 无 save-path lock waited 条目（今日）——拥堵是间歇性的，取决于大会话是否活跃。

## saveLocked 锁内段落地图（两套路由）

锁 = in-process save-path mutex（885 有界 30s）+ 跨进程 .lock（357 有界 5s）。锁内按序：

1. `requireWriteAuthorityForSave` — 快
2. `healEmptyCheckpointFromWAL` — 小 IO
3. `snapshotUpToDate` 快路径 — 固定小读（stat+ledger sidecar），未变版本直接跳过一切
4. `snapshotWithVersion` — O(n) 切片浅拷贝（6434 msg ≈ MB 级 memcpy，可忽略）
5. `probeLogForSave` — 头部小读
6. **schema-2 DAG 路由**（新日志默认）：
   - `dagStateForSave`：graph cache 命中 → 只扫新增字节（196 已治）；miss → 全量 replay（残留：nil_cache/generation 等 6 种 reason）
   - `planDAGWrite` → `diffDAGTranscript`：**第二个循环对全前缀每条消息做 messagesEqualForStorage = 双份 json.Marshal** → O(n) 次全量 marshal，锁内 CPU 大头之一（6434 msg × 2 marshals）
   - `appendSessionDAGEntries` — 只写增量（健康）
   - `writeDAGCheckpointCache`：pureAppend → 增量 append read model（健康）；**非 pureAppend（patch/systemChange/rewrite/fork）→ writeSessionMessages 全量 marshal+写盘**——工具预览更新（UpdateToolCallPreview→patch）在大会话上=每次 tool checkpoint 全量重写 .jsonl
   - `snapshotDigest`：版本变了 → `digestAndSizeSessionMessages` **全量 marshal+sha256**（55MB live ≈ 0.3~0.6s 推演）——append 保存每次付全量
   - `publishDAGDerived`：display index appendFrom>=0 增量 / 否则全量重建；head index + meta 小写
   - `maintainDAGLog`：oversized 才旋转（884 已治门）
7. **schema-1 路由**（存量老日志/非 lease 持有者）：
   - `classifySnapshotWrite`：writer-bound → writerTailDecision 小读 CAS（健康）；**drift/无 authority → checkSnapshotWrite → loadSessionUnlocked 全量 load 磁盘 transcript**（885 申报残留，锁内 O(文件) IO 的主犯）
   - `digestAndSizeSessionMessages` 全量
   - 非 append → `writeSessionMessages` 全量 + `appendSessionReplaceEvent` 全量
   - display index 全量重建

## 候选修法（量级依据 + 多维利弊 + 建议）

### A. classify 全量 loadSessionUnlocked 移出/消灭（schema-1 残留）
- 量级：全量 load = decode 整个 transcript（55MB live ≈ 秒级；728MB events 会话更甚）；触发条件 = writerTailDecision 失效（drift/无 authority/normalizedDirty/damaged）。
- 利：消灭锁内 O(文件) IO 主犯；弊：writer-bound 快路径已覆盖日常；fallback 是冲突语义的判据来源，挪锁外需防 TOCTOU（分类时点磁盘状态与写入时点不一致）。
- 建议：**保留在锁内但只在 drift 时付**；本件不动其位置，先靠 B/C/D 把日常路径的锁内成本压到底，drift 是罕见路径。

### B. 保存期 transcript digest 增量化（锁内 O(bytes) → O(delta)）
- 量级：append 保存现付全量 marshal+sha256；6434 msg 会话推演 0.3~0.6s/次（实测见 benchmark）；每次 version bump 都付。
- 修法：Session 内维护 sha256 增量状态（Go sha256 实现了 BinaryMarshaler 可克隆状态）；Add/AddBatch 纯追加喂新消息；Rewrite/Replace 失效重建；save 时状态有效则 O(1) 取 digest。
- 利：锁内 CPU 大头之一转 O(delta)，正确性由「状态版本号=version+rewriteVersion」钉死，失配一律回退全量（fail-open）；弊：mutation 路径多（session.go 十余处 version++），漏一处失效=digest 错=CAS 身份错——需逐点审计+穷举测试。
- 风险控制：状态失配条件全部 fail-open 回退全量重算，行为与现状 bit 级一致，只快不改。

### C. DAG diff 快路径（messagesEqualForStorage 双 marshal → DeepEqual 先行）
- 量级：diffDAGTranscript 第二循环 O(n) 次双份 json.Marshal，6434 msg 推演 ~1s（实测见 benchmark）；每次保存都付（含纯 append）。
- 修法：DeepEqual(相等) ⇒ storage 相等（真子集：全部字段相同则 identity fold 后 marshal 必相同）；DeepEqual false 才回退双 marshal 比较（ID/CreatedAt 差异等仍走原路）。零语义变化。
- 利：改动一个函数，纯加速，fail-open；弊：DeepEqual 本身 O(n) 字段遍历（但无分配，比双 marshal 快一个量级，推演）。

### D. 非 pureAppend 的 checkpoint 全量重写 → 锁外后台批写（dirty+flush）
- 量级：工具预览 patch 保存在大会话=每次全量 marshal+写盘 .jsonl（0.7~1.3s @96MB 磁盘，710 实测口径）；tool checkpoint 高频（每工具调用）。
- 利：最高频的大 IO 移出锁；弊：**语义改动最大**——.jsonl 从「锁内与 log 同代」变「滞后代」，直读读者的 stale 窗口扩大；shutdown/崩溃窗口的恢复语义要重新论证；后台 worker 生命周期管理。风险最高。
- 建议：**本件不做**，留另立项（需先有 D 的 stale-window 观测：read model 与 log 代差的现网频率）。

### E. display index / head index 锁内 → 锁外
- 已是 warn-only 可修复投影，但移动后与并发 saver 的代序交错需逐写者校验论证；收益小于 B+C（append 路径 index 已增量）。建议：本件不做。

### 本件取舍（铁律：改面大宁可分切片先做风险最低一条）

**做 B + C**（纯 CPU 出锁/减量，全部 fail-open 回退，bit 级行为不变），覆盖 schema-2 日常 append 与工具 patch 保存路径的锁内 CPU 大头；**A 保现状**（drift 罕见且是冲突判据）；**D/E 留另立项**（语义改动大，需先补观测）。

## 测试与护航计划

- 885 四锁测试 + 710 成本形状测试（append < full/2、redundant ≈0）必须全绿——B/C 只会更快不会更慢。
- 新增：digest 增量状态正确性（append/rewrite/replace/跨路径 save/并发 save 后 digest == 全量重算值）；diff DeepEqual 快路径与原语义等价（ID 差异/CreatedAt 差异仍正确分类）。
- benchmark：合成 4000 msg 会话分段实测（digest / diff / checkpoint write / display index），给出锁内各段量级表。

## 中断恢复

```bash
cd github-repo/worktrees/wt-923
go test ./internal/agent/ -run "TestSyntheticLargeSessionSaveCostShape|TestCrossSessionSavesDoNotShareLockKey|TestSameSessionSaveWaitChainBounded|TestBlockingSavePathWaitBounded|TestConcurrentSessionSavesBothBounded" -count=1
go test ./internal/agent/ -count=1 -timeout 20m
```
