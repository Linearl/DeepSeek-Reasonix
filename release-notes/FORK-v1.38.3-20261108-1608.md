# FORK v1.38.3-20261108-1608（包 85）Release Notes

> 基线=包 84（v1.38.3-20261108-1056，构建点 fe1c312e9）。本包六支，**主题=装机挂死故障根治**（诊断报告：docs/issues/reports/调研报告【非诊断】/装机挂死诊断-20261108.md）。构建点 `9fe34728b`。

## 🔧 装机挂死修复（mu-convoy 四刀，诊断定案逐项落地）

2026-11-08 装机版全局挂死（设置/切 tab/关窗全卡、重启无效）根因=协调线自检心跳回合 10:27 失控→保存写锁被占→RWMutex 写者优先使所有读者排队→前端重试放弃。四刀逐项根治：

1. **读路径与主锁解耦**（活动绑定 atomic.Pointer 快照化）——保存锁被占时设置/用量/指标/切 tab 等 9 个读者调用 <100ms 返回（修前读者车队 1.5s+ 阻塞实证），不再全局挂死。
2. **detached 后台回合墙钟上限**——无人值守回合默认 30 分钟强制收尾（`[agent] detached_turn_wall_clock_minutes` 可调，负值关闭），cause=`detached_turn_wall_clock_exceeded`，失控源头不再无界。
3. **in_flight_turn 启动恢复加年龄上限**——超过 2h 的残留「进行中回合」标记启动时终止态收尾（cause=`stale_in_flight_turn_recovered`），**治「重启也无效」**；新鲜标记恢复机器字节级不变（对照钉锁定）。
4. **无人值守回合内禁止宿主自重启**——失控第一因（心跳回合 11:03 执行 restart_update 自杀）根治：拒绝+cause=`mid_turn_host_restart_refused`，staged 装机目标不消费、回合结束后下一轮仍可执行；前台 autopilot 的 task-254 设计面不受损。

## 🔧 回合中自动存盘冲突不再当场换会话（tl1491，拍板点 6）

回合运行中 30s 自动存盘撞冲突时保持现会话对象+记诊断行（`conflict_recovery_deferred_turn_running`），冲突梯子（adopt/fork）推迟到回合边界收口——回合脚下的会话对象不再被中途换绑。

## 🔧 用户消息投递丢失三形态根治（tl1357，P0）

1. **受理即落盘**：steer 消息受理先走持久 inbox（blob+manifest 双提交）再注入——任何快照窗口/进程退出前消息已可检索可重放，真丢形态根绝。
2. **cancel 不丢引导**：turn 取消前快照已受理 steer，走 tl-1213 重放通道回队列自动派发。
3. **重启后排队消息自动继续**：恢复门自动清门扩展到有人值守 tab（四道保留闸不变：用户暂停/dial 关/纯检视架/中断工具评审），新增中性通知 `restart_gate_auto_continue`。

## ✨ 目标对象化（tl1472，P1）

goal 从「模式附属物」升格为独立对象（session-local）：phase（active/paused/blocked/complete/budget_limited）+activation（armed/disarmed）双字段，状态一致性契约测试锁定（armed+running⇒必续、disarmed⇒必不续）。**双通道创建**：新增 `create_goal` 工具，模型可自立目标（占位锁：已有未完成目标拒绝+可读 cause）；**边栏透明展示**：待办列表下 goal 卡（摘要+activation 双显）；disarm 全集（用户消息/stop/终态/预算耗尽/3 轮无进展）+每次变化打点。

## ✨ 后台 Bash 输出详情 tab（tl1479）

后台任务详情新增「输出」视图（用户视角去噪文本，非原始 JSON）：20 万字符尾部截断+跟随滚动显式开关+复制去噪全文+1s 自动刷新终态停轮询。

## ✨ Provider 轮询有界化+按需查询（用户两裁定直接立项）

1. **失败有界**：用量查询连续失败 3 次即停轮询+可见暂停标记「查询已暂停（连续失败 N 次）」，手动刷新/切模型恢复——禁「失败→重读 config→再失败」紧循环。
2. **无活跃会话不查询**：provider 用量轮询仅当存在使用该 provider 的打开 tab；全关时零请求零 config 读。alert 清理器每分钟路径改为缓存档位（零 config 读），断自反馈环。

## 预算棘轮（出包清障）

initial JS 667.9→669.6 / largest chunk 389.3→390.7 / zh chunk 128.4→129.7（均照实测+1.0）；initial raw 余 1.6、largest raw 余 13.8 未触线不动。

## 装机与验证

- 安装器：`desktop/build/bin/reasonix-desktop-amd64-installer.exe`（v1.38.3-20261108-1608）
- 或 staging 就绪：`C:\Users\yinji\AppData\Local\Programs\Reasonix\staging`（RestartAndUpdate 可发布）
- **装机前建议**：先按诊断 §6.3 清除失控回合标记（低风险，见诊断报告），或直接装包后验证——刀③会让超龄标记启动时自动收尾（notice `stale_in_flight_turn_recovered`），手动清除步骤可省略。
- **验证清单**：①启动后设置页可开且 60s 后仍可用（核心！之前几分钟后必挂）②左栏正常加载 ③心跳任务保持停用状态 ④「协调线自检」重新启用后 30 分钟上限生效（超长回合 30min 被收尾，cause 可见）。
