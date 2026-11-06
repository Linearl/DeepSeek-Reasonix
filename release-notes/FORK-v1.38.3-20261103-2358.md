# Release Notes — FORK v1.38.3-20261103-2358（包 71）

> 基线：上一时间戳包 v1.38.3-20261103-1825（包 70）。本包 = 今日 zcode 开发线十件滚合 + 预存红全量消解。
> 主线 tip：dae405ebe → 171950869 → f6b7ce84d（含全部 merge commit）。

## 缺陷修复（用户痛点直击）

- **归档强制真解锁**（tl1367）：「session is in use by another window」报错根治——残留锁（进程已死/pid 重用）强制归档自动撕锁一次成功；真占用明报「哪个程序、路径、几点占的」；报错中文转译+删除/关闭前移预检。归档被拒四线全落。
- **会话分叉 pinned 归属错配根治**（tl1371）：「pinned context belongs to session」报错读写两面根治——转正链改写归属（写侧预防）+ 读侧自愈（剥 recovery 后缀判同根自动回写，含副本对副本嵌套形态）+ 自愈落盘真 bug 修复（Windows sharing violation 重试必败）。**存量坏文件装机后首次读取自动修复**。
- **UI 发送 stale 三层**（tl1342）：发送遇「session write authority stale」自动 heal+恰一次重发（成功无感）；重发按钮「按了没反应」两机制根治（出生即 disabled+rewind 静默）；横幅多条未送达一键全部重发（拒绝条目保留）。
- **恢复横幅误报三修**（tl1372）：「检测到 N 条输入未进入对话」误报根治——折叠归档免报（查 repairlog）+ CAS 拒档归「待恢复」桶（实验开关默认关）+ dismiss 标记跨 fork 换名不丢。
- 侧栏长 provider 名错排 nowrap 修复；启动标题模型标识泄漏三处接线（tl1330）；版本副本面板死文案修正（tl1368）。

## 新特性/增强

- **主题第三组「媒体风格」+ Broadsheet Rose**（tl1343）：设置→外观→主题画廊新增组；报纸玫瑰风（三文鱼粉底+酒红强调），常规/工作台模式全局铺色，深色模式配套。
- **截图光标跟随**（tl1370）：多显示器下截图自动截鼠标所在屏，tl1088「全部显示器」开关行为不变。
- **版本副本删除**（tl1368 线2）：副本面板新增「删除选中对话」——主文件+全部副本+侧车一并进可恢复回收站，二次确认。
- **重启续跑交付链加固**（tl1366）：重启时排队任务等待+交付回执+落空重试+防重复投递。
- **准入矩阵代码化**（tl1340，实验开关 `experimental_collab_admission_gate` **默认关**）：协作消息准入策略表（4 源×3 名单×2@=24 格）代码门先于模型语义+投递全路径结构化日志+滞留 30min 告警。
- 时间预算测试确定性化（tl1355flaky）：chainScanClock 钩子+假时钟注入。

## 预存红全量消解（本包新纪律：分析后消解，不再申报绕过）

- agent `group rows 9 want 8` 两处（tl934-1 漏更期望）`1189b5770`
- desktop `BackendRejectsSubmit` nil panic（tl1266 桩缺覆写）`97a9f8861`；OutputStyle 两红（REASONIX_HOME 语义冲突）`c3b33d321`；ProviderDraftProbes（**真回归**：探针 draft-key 被 1308 活跃凭证静默换掉）`6e36761cf`；v3 heads 退役面四红（收敛契约改钉）`8cddfa906`；updater 下载页期望同步 `0b918045e`
- control `ConcurrentWriter` 两红（task 203 机制随 tl1280 退役，改钉新契约）`6cbf1a2c8`；`TailRewind`（夹具缩容避开 Diet 物理超窗触发线）`8a68411df`

## 已知边界

- 准入门默认关（需配置打开）；creation 模式浅色画布保留既定美学（pack 底色不上画布）；失败气泡重发走文本通道（与旧行为一致）；tl1296（lease 层根治）/tl1372 pending_recovery「从副本找回」操作面待后续包。
