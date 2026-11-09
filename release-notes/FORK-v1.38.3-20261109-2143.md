# FORK v1.38.3-20261109-2143（包 87）Release Notes

> 基线=包 86（v1.38.3-20261108-1608，构建点 0702de217）。本包 16 支滚合（12 支代码件+2 验收钉件+2 小件），**主题=13 件收尾批完成+停止链/循环响应性根治**。构建点 `b734d29a0`。

## 🛑 停止链/循环响应性两面根治（tl1514，P1 用户现场件）

循环打转（10m32s/51 工具）点停止无法终止的根治：
1. **任一停止入口武装升级梯子**——普通 Cancel 也吃 30s 保底自动升 force，不再永停 L1（此前只有 CancelStop 入口武装）；
2. **前端 grace 倒计时归零自动显「已终止」档**——不再依赖后端推送（publish 道 wedged 时视图永停 0s 的疑点①修复）；
3. **escalation 缺失 fallback 补兜底日志**（验收③不静默）；
4. **验收①循环场景钉**：停止后 ≤5s 回 idle（含循环场景）。

## 🔄 同形零进展循环打断（tl1514 面二=验收④）

running 内每个工具轮边界判「同形签名（工具名+参数）+零新 host 证据」连续达阈值 → 强制中断本回合+cause 可见（`NoticeCodeLoopBreaker`）。阈值：一般 5 轮/**steer 后收紧 2 轮**（弱模型不转向时的宿主兜底）；轮询白名单 {wait,bash_output,event_wait} 防误伤真实等待。与 tl1460 审查层分工清晰（轮末 vs running 内）。

## 💬 assistant 回复落盘 UI 不显示修复（tl1518，580 家族『没渲染』侧）

切走切回无效、关开 tab 才恢复的根因=switchTab 的 caller-skip 整体绕过 580 新鲜度门（轮已结束 running→idle 即漏）。修=持行切回必对账 sidecar 指纹（任一保存 bump revision），mismatch 走有界 latest-page 拉取。关开 tab 从现行 workaround 降级为 hydrate 异常兜底。

## 🧮 digest 算前编码规范化（tl1459F2，高风险安全侧地基件）

修掉表示漂移指纹分裂（go1.26 encoding/json 对非法 UTF-8 与字面 U+FFFD 编码不一致 → ledger digest 与重载重算天然漂移 → 写权限 CAS/快照 no-op 误判）。规范化只作用 digest 输入拷贝（存储字节不变）；合法数据 digest 零变化=存量账本零迁移（P1 钉证）；单 commit 原子可回退。

## 🎛️ 工作流定义管理面板+/workflow 命令（tl1300）

WorkflowPanel 加「定义」页签（列表卡片/查看/重命名/删除二段确认/运行）；`/workflow` 斜杠命令+参数补全；面板/命令/模板视图/agent 工具全走同一 store（SSOT）。编辑器不做（tl1454 模板视图已是完整编辑器，列候拍板）。

## 🧹 轮询治理（tl1496b 六项全扩+tl1329 审计）

- SettingsPanel 模型应用/BotRuntimeStatus：失败有界（3 失败停+暂停标记）+无渠道零调用；
- MCP 芯片：failStreak/paused 上限 3+tooltip 暂停行+焦点逃逸；
- detached_idle_release：5min tick 改读 task-609 config 快照（零 config 重读）；
- collab 三面板共享钟（每 host 单 timer，25 断言钉出站集合频率等价）；
- 审计全表：其余任务/协作/维护面逐项核对不适用（理由记录）。

## 🐛 其他修复

- **左栏相对时间四轮返修**（tl1485d）：活租约 behind 冻结戳自愈缺口（tl1459 busy 让位把打开中会话永久挡在自愈外）——装机后存量 behind 行首个 repair 波翻正；
- **移出侧边栏改首击 danger 确认弹窗**（tl1502b）+空项目移除失效修复（tl1502，rootGone 跳快照）；
- **投递不开轮最小修**（tl1339min）：paused 类 gate-held 静默停补口（WARN+计数）+dormant 队首可见化（零行为）；
- **任务列表 SWR**（tl1483）：刷新不再闪/跳顶（loading 门卸载已画列表+全量替换无 tie-break 两症状修复）；
- **Diet 超窗判据三段改造**（tl1506）：usage 实账主判据→计价兜底→废 base64÷4（含图会话不再误触发 Diet；推翻 1116 裁决记档）；
- **tl1357b**：parkedTurns 持久重放层+RecordUnappliedSteer 停靠行（数据丢失风险项）。

## 📝 文案与交互

- **自愈 Diet 通知**（tl1504）：补触发因/动作含义/数据安全（pre-repair 备份）三要素，路径引导改「设置 → 存储与路径 → 工作阶段版本与副本」；
- **「管理会话副本」改名**（tl1509）：设置→存储与路径按钮三语同步；
- **输入框两修复**（tl1508）：按钮行折行判定改实测 offsetHeight（661px 教训）+ResizeObserver 侧栏联动+分割线真生效（不再压建连钮）；运行状态一行化（窄态四钮折叠进「⋯ 工具」菜单+档位图标化+读数收展开层）；
- **右栏文件面板**（tl1510）：预览头补打开入口+六处截断面补原生 title；
- **1471b 删除护栏文案**：补「本拒绝面尚无 dry-run+字面目标逐个审计」真实机制引导（「every approval mode」经举证为 tl1471 有意保留的真话，未改）。

## 📐 验收钉件（tl1389/tl1397/tl1387）

三件均为「修复已全在基线」的验证收口（模型名显示两钉/上下文治理 3.0x 对照钉/子代理面板 340 钉认证）——装机复验缺口提醒已随板面报备。

## 预算棘轮

initial JS 672.3→673.5（贴线预抬）/initial raw 3296.0→3306.0（raw +10 步长）；largest chunk 392.8 余 0.5 与 zh/zh-TW/largest raw 未触线不动。

## 装机与验证

- 安装器：`desktop/build/bin/reasonix-desktop-amd64-installer.exe`（v1.38.3-20261109-2143）；staging 已铺。
- **重点复验**：①循环打转会话点停止 ≤5s 回 idle（或 LoopBreaker 自动断+cause 可见）②切走切回 tab 新回复即显示 ③左栏时间 behind 行翻正 ④空项目可移除+「移出侧边栏」弹 danger 确认框 ⑤工作流「定义」页签+/workflow 命令 ⑥输入框窄态恒一行+分割线不压钮 ⑦「管理会话副本」改名生效。
