# 包123 release notes — v1.38.3-20261129-0010

> 基线：包122（v1.38.3-20261128-1716，构建点 `afdfe9507b`）
> 构建点：`4653fb2b9e`（main-v2-stable 含 notes commit；七分支滚合+两主线直提；r1 前端 4 门红=node_modules 结构损坏，删整目录 offline 重装后 r2 一次过 rc=0）
> 执行线：zcode 开发线（GC 域首批由 zcode 新 GC 线承接——20261128 用户拍板 fork 侧撤销独立 GC 开发线）
> <!-- claims: feat-tl1861,feat-tl1862,feat-tl1859,feat-tl1756,feat-tl1866,feat-tl1867,feat-tl1747 -->

## 一、挂死治本与装配性能（用户四项拍板落地）

### tl1862 M1 desktop 全包挂死治本（122 分钟 → 26分59秒 实测 4.5×）
- TestMain 接线 PinResolveShellForTest：主短路+假探针兜底+sentinel 断言（probeBash 子进程清零，进程级实证）；全包实测 4592 pass/0 fail/exit 0
- M4 排程纪律（workspace AGENTS.md：全包永不三路并行+大包独占机器）+M5 known-failures 登记 8 条（checker 真实日志消费）+skill_pkg UserPromptSubmit 补 timeout 15000
- <10 分钟未达归因=固有非探测耗时（时敏等待/合法 bash 共 714s），处置候选留拍板

### tl1859 装配产物池键放宽 root-only（方案A，拍板丙第 1 件）
- runtimeAssemblyKey 去 model/effort 维度（四链六调用点同步）：同项目换模型/effort 打开不再全额冷建；组件级实测冷建 2.84s vs 复用 0.30s（10.5×），真机推演 boot 29.7→15-17s
- 跨模型复用 prompt 逐字相等断言（反向边界+多轮不累积）；同模型命中不回退+memory fingerprint 失配自动冷启
- **撞面修复 1 件预存缺陷**：collab index 段复用累积（复用 prompt 13826B vs 冷建 11074B 的 provider-visible 前缀漂移）→追加点幂等化（strip 旧段再追加，与 persona 同构）+同类扫描 boot.go 两处收口

## 二、GC 线（zcode 新 GC 线首批两件，联调 tl1854 交回）

### tl1866 J1 桌面无框根因翻案+观测增强
- **翻案：闸门判定正确无 bug**——桌面 tab bind 0624 全程锁文件未变，手机 takeover 指向另一对话 0608；spawn/resume 结构性接触不到 tab 内存租约（全链零写点）；J1 实跑成 J0 形态合法放行
- 放行/让出落空日志补 `desktop_holds` 字段（「判无」时报桌面实际持有会话，同类误诊一行闭环）；零语义变化不触安全边界
- 新增 bind→spawn→外部持 B 序列测试（B 放行无框+A 仍弹框 409+A 租约零触碰）；desktop 闸门组+serve 回归全绿
- **J1/J2-J4 复测指引**：认准手机列表 `heldBy:"other"` 条目（桌面真持有才弹框）；`heldBy:"me"`/空=J0 语义必放行

### tl1867 接管实例 slog 落盘（tailBuffer 退出即丢 → projects/<项目>/logs/serve.log）
- rollingLogWriter 照抄 desktop_log 算法（1MB/代×5 代，单项目 5MB 硬上限）+异步有界队列 64 块专写（stderr 永不被磁盘反压）+spawn 超时/启动失败/stop 三路收口 flush
- 出口建不起来自动退回 tailBuffer-only，spawn 永不因日志失败失败；slug 两域澄清（落盘用磁盘态 slug）
- 真二进制联调场景验证 49 行落盘可回放（boot 时序+每请求 slog 全 HIT）——J 类排查回放材料闭环

## 三、观察与盘点（rx 表格五件收口批）

### tl1747 投递可靠性观察打点+自动统计（P3 观察型，攒数据后定性）
- 三类路径全打点：failed_retrying 三 reason（enqueue_error/contact_pass_failed/session_open_failed，带 retry_attempt 序号+retry_cap）+exhausted（open_retry_exhausted 补 retry_cap=5/15/45s 三档）+cancel（每丢弃条一条，带 cancel_source/目标会话）
- 字段契约严格抄既有 logDeliveryDecision；cancel 生产路径仅两条全接线（同机制全量扫描）；机制零改动（diff 全为观察面）
- 统计工具 tools/delivery-reliability-stats 四类读数+按日表+-since+轮转支持；control 包级 1295 tests 全绿；数据积累 1-2 周后定性

### tl1756 门禁元数据退役盘点轮次2+tsc 基线批次（用户批 A）
- 轮次 2 建档：tsc 存量 572 对/380 文件+清零率 1.04%；fork-integrity 857 条/1944 锚全绿+轮次 1 五红全闭环+清零率 0.12%
- 批次落地（6e925b9287）：退役 3 对（tl1703 悬空 2+tl1776 stale 1）+登记 9 文件 17 对 23 条基线外（双树实锤=tip 预存漂移）；复跑 tsc-test-gate clean
- **进侧随件登记纪律双落**：政策 §4.6+AGENTS.md 第 10 条（新增测试面件随件报数候拍板，刷新后 3 天漏 23 条判例）

## 四、冻结实施（tl1826 拍板线）

### tl1861 C-2 冻结实施数据（可达性门离线+104 文件冻结清单）
- C-2 冻结实施数据产出；C-3 围栏手术与 M3 Defender 排除维持 block（候 M1 效果观察/冻结数年后）

## 五、文档

- docs-site changelog 补 11-25~28 八包欠账（115-122）+index 计数同步；v8 changelog 草稿（10-11→11-28 约束 130 包聚类七主线）——v8 定稿以包123 装机验收通过为前提

## 验收提示（硬门：出包 → 装机验收 → 才 release）

- B 组式实测重点：tl1859 池键放宽的切换/启动面 + tl1862 四件（全包测试时长+known-failures）
- 「编队齐→出包→验收过→release」四步缺一不可；有问题先返修（123 返修包）→再验收→才 release
