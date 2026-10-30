<!--meta title="更新日志" desc="10.12 起的版本时间线：正式 Release 与内部开发包分开标注，同日多包折叠。" -->

# 更新日志

本页记录全部版本：**10-12 起的内部开发包逐包列出**（同一天多包折叠为一组）；**正式 Release 自 2026-08-25 v1.31.3 起全量收录**在页末「早期里程碑」区（含更早大版本）。更早的内部开发包不逐包列出，需要细节见仓库 [`release-notes/`](https://github.com/Linearl/DeepSeek-Reasonix/tree/main-v2-stable/release-notes)。

**两类版本的区别**：标 <span class="vtag vtag-release">正式 Release</span> 的是发布到 GitHub Releases 的版本，可从 Release 页直接下载安装；标 <span class="vtag vtag-internal">内部包</span> 的是持续集成的开发快照，未经发布流程，仅供内部验证与升级链路使用。本 fork 的开发节奏是每个工作日可能出多个内部包，正式 Release 按里程碑单独发布。

**统计**：10-12 起共 29 个开发包（正式 Release 1 个、内部包 28 个）；另有早期正式 Release 10 个（v1.31.3 至 20260921-1600）见页末。

<div class="timeline">

<section>
### v1.38.3-20261030-0139 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">0139 构建</p>

- 本版是 **拖拽提示统一 + 文档站收口批**：①侧栏拖拽放置提示统一；②侧栏之外三处拖拽提示扩面统一；③模式下拉收起态定宽收口；④产品文档站基址修正+入口回填。

</section>

<details class="day-group">
<summary>2026-10-29 <span class="gcount">· 8 个包</span></summary>

<div class="inner">
<section>
#### v1.38.3-20261029-1802 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">1802 构建</p>

- 本版是 **滚动锚定与清理根治批**：①侧栏展开/收起时聊天不再跳动；②筛选面板重构（改名+重排+记忆折叠）；③项目树空壳会话两阶段根治；④简洁模式思考行消失根治。

</section>
<section>
#### v1.38.3-20261029-1658 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">1658 构建</p>

- 本版是 **批量操作与报障清障大批**：①侧栏批量会话操作（归档/移动一次一键）；②workbench 头部对齐 classic；③删除分组死按钮根治；④心跳任务行内暂停按钮；⑤E 开关迁实验室；⑥群聊邀请面板/搜索面板两处交互修复；⑦文档站内容补齐。

</section>
<section>
#### v1.38.3-20261029-1054 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">1054 构建</p>

- 本版是 **用户报障清障批**：①autopilot 切换失效根治；②pinned 记忆职责迁移（启动提速）；③聊天贴底随机失效修复。三项均用户实锤报障后定位根治。

</section>
<section>
#### v1.38.3-20261029-0803 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">0803 构建</p>

- 本版是 **群聊解冻 + 测试面清偿批**：①群聊两大死路根治（邀请检索失效+群主退群死锁）；②desktop 预存红 26+19 件退役清偿（测试耗时 463.6s→23.2s）；③DAG 退役收尾销案。

</section>
<section>
#### v1.38.3-20261029-0616 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">0616 构建</p>

- 本版是 **交互细节批**：①侧栏会话右键菜单整理（9→10 项+二级聚合）；②能力注入 hover 明细完整化；③任务面板 hover 命令截断修复。

</section>
<section>
#### v1.38.3-20261029-0414 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">0414 构建</p>

- 本版是 **性能双达标 + UI 细节修批**：①大会话冷启动性能 P0 达标（resolve 239.5ms→61.1ms、内存 147.8→5.1MB）；②纯 CSS 双件修模式下拉跳变与换行图标；③信件族双件修角标与懒加载。

</section>
<section>
#### v1.38.3-20261029-0133 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">0133 构建</p>

- 本版是 **DAG 退役主线第二阶段批**：①存量 DAG 索引一次性无损迁移+读侧单路径化（296 个会话全量校验零失败）；②DAG 存量观测计数落地（普查 CLI+nil_cache 计数）。

</section>
<section>
#### v1.38.3-20261029-0115 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">0115 构建</p>

- 本版是 **multi_edit 增强与能力可见性批**：①multi_edit 三条增强全部实验开关化（默认关、行为零变化）；②能力目录预算扩容临时缓解——修复「多数能力对模型不可见」的截断缺陷。

</section>

</div>

</details>

<details class="day-group">
<summary>2026-10-20 <span class="gcount">· 2 个包</span></summary>

<div class="inner">
<section>
#### v1.38.3-20261020-0930 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">0930 构建</p>

- 本版是**大会话防护与运行时健康批**：①大会话三重防护——超长会话加载不再转圈、带图超窗给出指名提示与分块救援、进度漂移只提示不误切；②会话健康检测与一键自愈进入设置页，损坏会话可图形化修复；③会话运行态五态单源化，排队/停止等状态显示不再互相打架；④模型音频输入与自发延时消息两项能力落地。无数据…

</section>
<section>
#### v1.38.3-20261020-0030 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">0030 构建</p>

- 本版是**实验室档位改造 + 会话体验修复批**：①实验室新增「稳定」档与批量开关，核心能力默认锁定、进阶用户可解锁关闭（本次最大特性）；②write gate 门禁移除，AI 工具写操作不再被契约门禁拦截；③收件箱单条上限调档 16MiB，大文件传递不再被 4MiB 拦截；④项目栏闪烁二轮根治与 …

</section>

</div>

</details>

<section>
### v1.38.3-20261019-1008 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">1008 构建</p>

- 本版是**项目栏与全局体验修复批**：①项目栏闪烁与 Global 会话冒泡两类「界面自己动」问题根治；②组内会话拖拽排序真正接线（此前拖了没反应）；③一批界面细节收尾——层级弹窗 z-index 修正、sha512 校验、三语言提示补全、详情弹窗全文查看、实验室条目重组；④按用户指令对历史遗留测试…

</section>

<details class="day-group">
<summary>2026-10-18 <span class="gcount">· 2 个包</span></summary>

<div class="inner">
<section>
#### v1.38.3-20261018-1137 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">1137 构建</p>

- 本版是**能力架构渐进披露首发 + 对话内产物卡片 + 一批界面与稳定性收尾**：①AI 的工具目录全面改版——默认摘要档一次看全、53 个工具分成八组可查「组卡」、九个会话协作动作收拢为统一入口（后三项为实验特性，默认关闭，实验室开启）；②对话里可以直接渲染 Markdown/文本报告卡片并一键打…

</section>
<section>
#### v1.38.3-20261018-0528 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">0528 构建</p>

- 本版是**能力架构渐进披露首发 + 对话内产物卡片 + 一批界面与稳定性收尾**：①AI 的工具目录全面改版——默认摘要档一次看全、53 个工具分成八组可查「组卡」、九个会话协作动作收拢为统一入口（后三项为实验特性，默认关闭，实验室开启）；②对话里可以直接渲染 Markdown/文本报告卡片并一键打…

</section>

</div>

</details>

<section>
### v1.38.3-20261017-0646 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">0646 构建</p>

- 本版是一次**稳定性大修 + 两个新能力**：①修掉了两类"会话彻底卡死"——**转录门毒丸**（一条畸形工具参数让会话永久报错，本版装机后中毒会话自动恢复）和**保存锁互相等待**（跨会话保存互卡秒级→有界超时），另有"发消息没反应 / 设置页转圈 / CPU 飙高"的本地风暴源修复（关标签不再拖…

</section>

<details class="day-group">
<summary>2026-10-16 <span class="gcount">· 2 个包</span></summary>

<div class="inner">
<section>
#### v1.38.3-20261016-2039 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">2039 构建</p>

- 本版是一次**稳定性大修 + 两个新能力**：①修掉了两类"会话彻底卡死"——**转录门毒丸**（一条畸形工具参数让会话永久报错，本版装机后中毒会话自动恢复）和**保存锁互相等待**（跨会话保存互卡秒级→有界超时），另有"发消息没反应 / 设置页转圈 / CPU 飙高"的本地风暴源修复（关标签不再拖…

</section>
<section>
#### v1.38.3-20261016-1040 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">1040 构建</p>

- 本版是 **压缩韧性 + 上游同款修复批（十九件）**：①超长会话压缩失败「永久放弃」根治（不再无重试无兜底）；②八件上游同款修复移植（含两件 data-loss 级）；③右下角弹窗统一、multi_edit 注入面、跨会话回执分级通道等一批工程与体验件。

</section>

</div>

</details>

<details class="day-group">
<summary>2026-10-15 <span class="gcount">· 3 个包</span></summary>

<div class="inner">
<section>
#### v1.38.3-20261015-1528 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">1528 构建</p>

- 本版是 **data-loss 返修 + todo 持久化 + 两项基建落地**：①项目分组归属被静默清空的回归根治（P0）；②会话 todo 跨回合不再被宿主清空；③EBDD 评测驱动研发框架四阶段与 dynamic-workflow 流水线编排 S0-S3a 落地。

</section>
<section>
#### v1.38.3-20261015-0641 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">0641 构建</p>

- 本版是 **批 29 收尾（两件）**：①710 类病灶体量哨兵——会话文件与 events 达阈值时主动告警并给出转线建议；②项目分组支持任意层级嵌套的 UI 片（递归渲染 2→64 层）。

</section>
<section>
#### v1.38.3-20261015-0548 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">0548 构建</p>

- 本版是 **夜战大批（十七件）**：①四类卡死/误判根治（events 折叠失效、save 锁互等、wait 工具误判卡死、切模型被后台任务拦）；②sentinel 穿透封堵（24 路径，实验开关默认开）；③新建会话自动开轮、跨会话折叠稳定性、capability 纠错等一批可用性修复。

</section>

</div>

</details>

<details class="day-group">
<summary>2026-10-14 <span class="gcount">· 3 个包</span></summary>

<div class="inner">
<section>
#### v1.38.3-20261014-1958 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">1958 构建</p>

- 本版是 **压缩可见性与用量面板修复批（三件）**：①压缩失败不再静默，三态显性化；②模型热切换时旧 provider 用量卡即时下线；③bundle 预算棘轮预抬。

</section>
<section>
#### v1.38.3-20261014-1900 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">1900 构建</p>

- 本版是 **会话可靠性与渲染修复批（八件）**：①渲染层瞬时丢失对话内容修复；②切 tab 就绪 16s 的 ancillary 腿治理（P0）；③恢复确认门不再卡住无人值守会话；④ask 工具在 autopilot/yolo 下的可达性收口；⑤崩溃回放 payload 伪造三层核实。

</section>
<section>
#### v1.38.3-20261014-0149 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">0149 构建</p>

- 本版是 **低峰批（六件）**：①表情包发送（8 分类 + 搜索）；②文件面板操作体验包（右键三项 + 回收站 + 多选批量删除）；③项目分组四件（重命名/解散/字号 token 化/图标列与拖拽换序）；④实验室 UI 完整性补全与证据门熔断改进。

</section>

</div>

</details>

<details class="day-group">
<summary>2026-10-13 <span class="gcount">· 2 个包</span></summary>

<div class="inner">
<section>
#### v1.38.3-20261013-1451 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">1451 构建</p>

- 本版是 **批 24.4（七件）**：①sentinel 只读命令误拦修复（grep/cat 等不再被当写操作拦截）；②会话分组实施件落地——协作中心首版上线；③两件 data-loss/可用性类排查与根治（过程折叠回归、崩溃分析误改用户会话标题）；④`versions` 目录自动清理（更新后保留最…

</section>
<section>
#### v1.38.3-20261013-0014 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">0014 构建</p>

- 本包以**体验还债**为主题：①**样式 token 化专项**——全局字号设置响应覆盖率从 18% 提到 60%+，圆角收敛 3 档，按钮体系归一（直接回应「设计语言混乱、字号随意」的社区反馈）；②**启动竞态修复**——启动后 tab 未就绪时打的字不再消失（composer 草稿 key 竞态…

</section>

</div>

</details>

<details class="day-group">
<summary>2026-10-12 <span class="gcount">· 4 个包</span></summary>

<div class="inner">
<section>
#### v1.38.3-20261012-2359 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">2359 构建</p>

- 本包以**体验还债**为主题：①**样式 token 化专项**——全局字号设置响应覆盖率从 18% 提到 60%+，圆角收敛 3 档，按钮体系归一（直接回应「设计语言混乱、字号随意」的社区反馈）；②**启动竞态修复**——启动后 tab 未就绪时打的字不再消失（composer 草稿 key 竞态…

</section>
<section>
#### v1.38.3-20261012-1748 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">1748 构建</p>

- 本包修复 **tab 宽度档位锁死**（graphite 主题下拉宽/最大化/跨屏不再卡在收缩档位）与 **ask 审批面板不可达**（可逆操作也弹面板、面板内操作停止倒计时、无操作 15 秒自动代答）两个日常高频卡点；新增**群聊视图**（多会话合流消息流）；**超大长会话投影失效从「全量回退」改…

</section>
<section>
#### v1.38.3-20261012-1730 <span class="vtag vtag-internal">内部包</span>

<p class="tl-date">1730 构建</p>

- 本包修复 **tab 宽度档位锁死**（graphite 主题下拉宽/最大化/跨屏不再卡在收缩档位）与 **ask 审批面板不可达**（可逆操作也弹面板、面板内操作停止倒计时、无操作 15 秒自动代答）两个日常高频卡点；新增**群聊视图**（多会话合流消息流）；**超大长会话投影失效从「全量回退」改…

</section>
<section>
#### v1.38.3-20261012-0045 <span class="vtag vtag-release">正式 Release</span>

<p class="tl-date">0045 构建</p>

- 本版集中解决了**跨会话消息投递的一整类可靠性问题**（偶发丢失、重复投递、静默不唤醒、收件箱显示异常）与**会话「假运行」问题**（异常退出后卡在运行态、新消息被吞）——收件箱面板从此全面可用；**切换模型/档位从 10 秒以上降到 3 秒级别**；**大会话切换从 71.7 秒降到 3 秒上下（…

</section>

</div>

</details>

<details class="day-group">
<summary>早期里程碑 · 正式 Release（2026-08-25 ~ 09-21）<span class="gcount">· 10 个</span></summary>

<div class="inner">
<section>
#### desktop-v1.38.3-20260921-1600 <span class="vtag vtag-release">正式 Release</span>

<p class="tl-date">2026-09-21 发布</p>

- 内存战役版：v4 快照 payload 有界化（96MiB 上限）+ transcript 几何哈希缓存，桌面版内存从 2~4GB 降至 700~800MB 基线。

</section>
<section>
#### desktop-v1.38.3-20260916-1205 <span class="vtag vtag-release">正式 Release</span>

<p class="tl-date">2026-09-16 发布</p>

- 版本号开始携带构建时间戳（区分同基线多包）；切模型后上下文投影失效修复。

</section>
<section>
#### desktop-v1.38.3 <span class="vtag vtag-release">正式 Release</span>

<p class="tl-date">2026-09-15 发布</p>

- 追齐官方 v1.38.3 基线（fork 里程碑：此后以 1.38.3 为底座持续演进）。

</section>
<section>
#### desktop-v1.38.1 <span class="vtag vtag-release">正式 Release</span>

<p class="tl-date">2026-09-09 发布</p>

- 追齐官方 v1.38.1（图片硬编码三层清除 + classic 迁移收尾）。

</section>
<section>
#### desktop-v1.34.0+fork <span class="vtag vtag-release">正式 Release</span>

<p class="tl-date">2026-09-07 发布</p>

- fork 特性叠加版（+fork 后缀起点：经典布局保留 + fork 专属功能叠加在官方 1.34 之上）。

</section>
<section>
#### desktop-v1.34.0 <span class="vtag vtag-release">正式 Release</span>

<p class="tl-date">2026-09-07 发布</p>

- 追齐官方 v1.34.0。

</section>
<section>
#### desktop-v1.33.0+fork <span class="vtag vtag-release">正式 Release</span>

<p class="tl-date">2026-08-31 发布</p>

- fork 增强批：effort 切换双 PR、思考档位通用化（非 DeepSeek 模型适配）。

</section>
<section>
#### desktop-v1.31.4+fork <span class="vtag vtag-release">正式 Release</span>

<p class="tl-date">2026-08-28 发布</p>

- locale bundle 预算放宽（降级 warning + 硬上限）+ 标准模式混合指令拦截。

</section>
<section>
#### desktop-v1.31.4 <span class="vtag vtag-release">正式 Release</span>

<p class="tl-date">2026-08-26 发布</p>

- 实时交互 + 跨模型兼容 + 首次安装体验增强。

</section>
<section>
#### desktop-v1.31.3 <span class="vtag vtag-release">正式 Release</span>

<p class="tl-date">2026-08-25 发布</p>

- fork 首个正式 Release：并行写入、子代理三档、长会话压缩阈值、会话秒切。

</section>
</div>

</details>

</div>

> 内部包不逐条列改动清单；每个包的完整条目见仓库 `release-notes/FORK-*.md`。
