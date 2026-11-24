# 包112 release notes — v1.38.3-20261124-0820

> 基线：包111（v1.38.3-20261123-1518，构建点 520cf8bb08；含合并线夜批收编的 tl1751/1752/1739r2/1741r2 尾项/tl1783/1784/1785/1770r2 等）
> 构建点：`89a123dae6`（main-v2-stable，五支滚合后）
> 产物：`desktop/build/bin/reasonix-desktop-amd64-installer.exe`
> 执行线：zcode 开发线（20 支 merge 全量本包，zcode 滚合）

## 一、修复（P1/P2 主力）

### tl1770 拍板实施：压缩观测增强+全量送 D+超窗策略（P1）
- **观测增强**：summary 请求 cache 读数 slog（prompt_tokens/cache_hit/cache_miss/cache_write/ttft/duration）——800K miss vs hit 成本差几十上百倍从此可对账
- **全量送（含 D 尾部）**：常规压缩单发腿请求=整视图+追加指令（codex 同形，前缀缓存全程覆盖）；focus 选段道不动
- **超窗从头删保前缀 cache**：replay-safe unit 从最旧端删（保护 system/compaction-summary/最新 unit）
- **分桶日志**：压缩前后 7 桶构成（系统/真用户输入/session-context 载体/assistant/tool/摘要/其他）——「其他 205K」黑桶拆开见人
- 三个旧断言随拍板换代；任务 719 防线保留（hook 吹大仍拒绝）

### tl1786 崩溃归因+黑匣子（P1 最高优先）
- **归因结论**：16:27 exit=1 三通道零痕迹+仓内所有 exit=1 路径都先写日志 ⇒ 唯一吻合=外部 TerminateProcess 类击杀（非 1739r2/repair 链）
- **黑匣子三件**：launcher last-abnormal-exit.json 富化五字段（reason/stack/lifecyclePhase/wedgePoint/crashFatalSeen）+desktop decoder 锁步+webview2 插桩附调用栈——下次无声退出死亡瞬间写证据一眼定谳

### tl1783 哨兵路径大小写绕过（P1 安全）
- normalizePath 补 Windows 平台大小写折叠（保护路径注册与用户输入同函数自洽）；CONFIG.TOML 变体穿透 write_system_config 硬拦截口子闭合；POSIX 逐位不变
### tl1784 反风暴闸 UTC 截断（P1）
- Truncate(24h)→本地日界重组 midnight——东八区每天 00:00-08:00 发信上限失效 8 小时口子闭合；FixedZone 注入测试零依赖宿主时区

### tl1785 投影 hash 失配降级保留+tver 恢复链
- covered-prefix hash 失配不再全丢回退 2M 全量：三闸（逐消息 corpus 在/管线等长/分歧槽全落 assistant·tool）全过保留 body 标 stale；真分歧仍 fail-closed
- tver 计数器重启归零修复（FloorTranscriptVersion 单调 floor）
### tl1739r2 epoch-fence 重复渲染复发返修
- 渲染入口按 turn id 去重（reconcile 重放不重复 append）——复发机制链坐实（fork 吞 turn_done 后账本归零水位线重灌）

## 二、修复（续）

### tl1774 provider base_url 协议路径校验与提示（issue #105）
- 裸 base 保存触发示警（三协议后缀形态）+「补全」按钮一键改写+404 错误文案附指引（四协议）；正确填写零打扰
### tl1777 输入框溢出复发
- 附件条高度入纵向预算（360 钉值不动）+图片加载完成/侧栏收窄触发重测（ResizeObserver 共用机制）——「条+卡」总高不再失控
### tl1769 hover 完整对话名
- 悬停第一行恒完整对话名（anywhere 换行），消息预览/状态/职责依次排后

## 三、治理/清理

### tl1782 测试面孤儿删除+X2 收编
- 三孤儿 helper+一次性探针删除（双 grep 零残留+CI 计划 978 零差异）；stackingOrder.test.ts 纳入 CI（零覆盖生产模块补 4 断言钉）
### tl1780 同构合并压平五组
- E3 频道读双胞胎/E4 bot 渠道镜像/S1 四级链压平/T1 sink 双胞胎合并——净 −33 行；C2/C3/C4 确认 tl1754 已覆盖不重做
### tl1781 微包装与杂项十一条
- 19 改/3 不改列明（U4 End 幂等=有意设计候拍板；K8/U5 观察档）；净 −176 行
### tl1779 上游继承兼容层删除
- C5-C28 十二处死码+3 配套测试+transport_sse.go 整文件——净减 286 行；C14 两案候拍板；C1 与 tl1754 重复只做对账
### tl1788 环境误报治理（本包新落地基建）
- **统一 tsx 测试入口** tsx-test.mjs（css/svg stub+扩展名自动纠偏——ERR_UNKNOWN_FILE_EXTENSION 家族根治）
- **harness 环境误报自动甄别**：失败即同参单跑复验（绿=ENV-FLAKY 留痕不阻断；再红=真红阻断）+jsdom 预飞检查（坏环境一条报错替代 287 大面红）
- **go-test-triage.sh**：失败包自动隔离单跑 3 次（FLAKY 不阻断/REAL-RED 阻断留证）
- **Makefile go test 超时 10m→30m**（agent 包 750s+/desktop 全量 1203s 不再被误杀）
### tl1789 基线红治理（结构性收获）
- **desktop 包从一跑就崩治到第 5 跑 EXITCODE=0 全绿**（4558 支：3715 PASS+47 SKIP+0 FAIL）——崩掉的半截里藏着 19 支从没人见过的老红全部顺手治完
- 32 支修绿+12 支挂牌（隔离不拦出包）+55 族归档候拍板；provider 时序挂账遵任务书
- tl1652 manifest 欠账修复（合并线 41685f4dc6 先行，本包含其验证面）

## 四、装机验证清单（reasonix 开发线逐项）

1. tl1741r2：实验室 8 卡可见无主开关**行观感正常不置灰**、子项可读写、fullAccess 唯一主卡
2. tl1746r2：任务面板三分布卡彩色填充
3. tl1539：serve 面板自定义地址→tunnel QR→GC 扫码连接（联调）
4. tl1721：群详情清空/恢复；AI channel_invite 拉人正反例
5. tl1765：异常态停止三档可用；无 label changed 误报
6. tl1763+tl1771：Shell 检测双检出；**重启后二次启动日志见 cache-hit**
7. tl1751：压缩过程实时 tokens/tps；hover 百级真值
8. tl1752：重启后上下文卡直接「实测」
9. tl1762：edit_file 证据门不死锁（read_file 尾部 source_token）
10. tl1767：**真机 glm-5.3-flash 勾视频拖 mp4 可描述**
11. tl1768：mailbox batch_dispose 两段式+UI 多选批量
12. tl1772：实验室「决策模式提取」开关两态
13. tl1769：hover 第一行完整对话名
14. tl1770：压缩前后分桶日志+cache 读数出数
15. tl1774：裸 base 保存示警+404 文案指引
16. tl1776：贴底态流式输出视口跟随不回弹
17. tl1777：侧栏展开+图片输入不溢出卡外
18. tl1783/1784：哨兵变体拦截+反风暴闸时区
19. tl1785：装机重启投影降级保留（不再 2M 全量）
20. tl1786：崩溃时 last-abnormal-exit.json 含 reason/stack
21. tl1789：desktop 包 `go test .` 全绿
22. tl1713 低收益门装机观察（已拍板默认值）

## 五、候拍板/移交

- tl1770 甲案（save/heal 域数据完整性单独开件）+乙案（tl1651 migrate 门受控通道——**若立项需与 tl1785 projectionRepairDriftKeep 三闸对表**）
- desktop 15 环境敏感预存红候派单（tl1789 治理后残余已收敛）；包级超时（1788 已治 30m，desktop 全量候 45m 或拆包）
- tl1754 七项+tl1756 退役候选（4 换代+1 退役）候拍板；tl1780 T1 死双胞胎确认死候单独删
- tl1746 二期+tl1733 Block2（12 月初）+tl1735 B2-4 维持例外+tl1730 Go 发射点手术——候拍板
- tl1103 ledger 未跟 tl1711（tl1769 发现）候派单；tl1787 lineage 只读备份成员档拆件候拍板
- tl1777 同机制遗留清单（报告 §4）候拍板
- tl1722 验证指引：A 方案=复用「决策模式提取」开关两态（非新独立开关）——候用户验证
