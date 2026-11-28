# 包120 release notes — v1.38.3-20261128-0916

> 基线：包119（v1.38.3-20261127-2143，构建点 `f7c408fe3b`）
> 构建点：`9e327c64ae`（main-v2-stable；zcode 侧五支+三语文案搭车件；三轮 rc=0——r1 node_modules 掏穿重装/r2 签名门抓 wailsjs 过期 regenerate+棗轮六线超线实测抬齐/r3 干净过）
> 执行线：zcode 开发线（本批五支全量；reasonix 侧 9 件其线节奏另批并包，见候拍板）
> <!-- claims: feat-tl1838,feat-tl1827d,feat-tl1845,feat-tl1835,feat-tl1837 -->

## 一、P1 修复

### tl1845 主对话流整轮漏画根因修复（用户定性严重，全链取证修正推测链路）
- 任务书推测（hook 超时阻断入档）只对三分之一——**UserPromptSubmit 纯观察 block 恒 false 不阻断**；真根因①=对账竞态撞 in-flight 入档窗口产生假横幅（beginCheckpoint 在 PromptSubmit hook 前开档，消息等 hook 返回才 append→checkpoint 有 prompt 而 snapshot 无消息=误判 GapUnfiled）；真根因②=表面缺转后无自愈回读通道（重开 tab 是唯一 hydrate 途径）
- 主刀=run 活跃期豁免最新一条 checkpoint 的 gap（掐假横幅源头；真缺口照报 1023/1372 零变化）；副刀=横幅条目签名变化且表面安静时走 retrySessionHistory 一次回读（与重开同源 hydrate）
- 数据层零丢失实锤（检查点 253/261+diet 备份 741 条含完整两轮）；红测试三处先证后绿+control 358s/hook 24.7s 全包绿+surfaceAdmission 钉零回退+unfiled-inputs 61/61
- 附带：hook 超时文案 scope 感知（plugin 作用域指 manifest 条目不再误导 settings.json）

## 二、新功能

### tl1835 goalMode 激活确认预算弹窗
- 开关 goal_budget_confirm 默认关（关=直通现状逐字节不变）；显式激活入口弹窗展示目标名+预设档 1/2/5/10M+自定义，确认落地照常激活（live 镜像免重启），取消回填草稿不激活；心跳自动激活不经弹窗；弹窗复用 lab-pick-dialog 壳零新 CSS；前端钉 50/50+Go 2/2

### tl1837 压缩折叠关键读数持久化（案B 关联件）
- 每会话 sidecar `<stem>.compactmetrics.jsonl`——desktop.log 轮转不再丢读数；案B 主疑点（fold 计划 1384 vs 实际 329）计划/执行两半边跨轮转可查
- 5 kind 字段与日志锚逐字段同源（fold_plan/fold_done 含 buckets_pre/post 全分项/fold_declined 拒因含低收益门/fold_failed）；流式 O_APPEND 不阻塞主路径；512KiB∩尾部 256 行双界滚动
- **四链登记同 commit**（store 常量+备份白名单+sweep 形态表——第四条消费链被 shape-table 门禁拦下后补接+integrity 锚 4 条）；读接口 ListSessionCompactMetrics；spellings 预存红顺手收口

## 三、改善/文档

### tl1838 todo_write 说明分层重构+丢失恢复指引
- 6125B→5374B（-12.3%，同口径-18%）；四段结构（QUICK RULES 高频拒绝前置>LIST LOST 新增>EDITS+示例>DETAILS 低频后置）
- **补丢失恢复四短语**（重启/压缩后先 todo_read 重建+逐字复制 step_id/带回 plan_id+空 baseline 合法——当日 4 次 rejected-as-invented 的预防）；字节上限钉 2400 新增不瘦身即红；既有钉原句全保留（误伤 3 短语被测试当场抓住恢复）
- 宿主侧 todo 丢失归 feedback-20261127-042715 另案（边界清晰）

### tl1827d doctor repair --undo 文档补齐（tl1827 全闭环）
- 本体（7fb51c5da7 整事务回滚）昨夜已在主线——任务书抽核误判过时，本件补 RECOVERY.md/zh 双语 --undo 用法+语义段（+41 行：命令表/redo 副本机制/rollback_update 排除说明）

## 四、搭车件

- 三语 dagCache 文案微调（用户 reasonix 侧改代提交，f54eae070a）：「图缓存容量」改「会话回放缓存容量」+hint 补「save 性能关键，非 DAG 专用」消歧

## 五、装机验证清单（reasonix 开发线逐项）

1. **tl1845（重点）**：hook 超时场景（挂 computer-use bat）发消息→消息正常入主流+假横幅不再出现；如仍见「未入档输入」横幅且消息确实丢了请回报
2. tl1835：实验室开 goal_budget_confirm→激活 goal 弹窗→改预算确认生效/取消不激活；开关关=现状不变
3. tl1837：触发压缩后 sessions 目录出现 `<stem>.compactmetrics.jsonl`（字段与日志锚同源）；后续压缩效果调查有数据可用
4. tl1838：todo 丢失场景按新指引 todo_read 重建不再被判 invented
5. tl1827d：`doctor repair --undo` 用法见 RECOVERY.md
6. 三语文案：设置缓存调优页「会话回放缓存容量」新文案

## 六、候拍板/移交

- **reasonix 侧 9 件其线节奏**（tl1846/1847/1848/1851/1854/1855+1828/1800/1741 余项）——已确认转 zcode 执行，zcode 正在派单，随下批并包
- tl1853 三项候拍板：①tl1850r2 恢复 guardian.rewritelog 保护位（误判回归，建议必做）②rotating 死键+~370 行死代码删 ③subagent 15 键收窄（低危可缓）
- skill_pkg manifest UserPromptSubmit 补 timeout 15000 一行（tl1845 遗留，候拍板）
- 包119 装机验证回板待 reasonix 线（重点 tl1843 heal WARN 观测/tl1833 outline 试读/B3 排序重启复测）
- 预存红 3 件候 known-failures triage；tl 回写归协调线（包119 七支+包120 五支）
