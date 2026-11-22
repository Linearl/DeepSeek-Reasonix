# 包110 release notes — v1.38.3-20261123-0320

> 基线：包108（v1.38.3-20261122-1457，构建点 7fa4d346c3）
> 构建点：`471327b9c6`（main-v2-stable，包110 棘轮 commit 随）
> 产物：`desktop/build/bin/reasonix-desktop-amd64-installer.exe`（74,076,301 B）
> 执行线：zcode 开发线（合并线另收 1718/1743/1751/1752/1746r/manifest 修复；其余 12 支 zcode 滚合）

## 一、返修（包108 两项打回的纠正）

### tl1741r2 实验室面板「卡体保留、主行撤」
- 包108 撤卡语义纠正（用户裁定：清主选项≠砍整卡）：8 张 stable 卡**回归实验室 rail（无主开关）**，子项全部恢复可配置（autopilot 问询超时/五类授权/轮次上限、sessionCollab 十项、资源治理整卡等）；fullAccess 仍是唯一带主开关的 stable 主卡
- config 手写主键 false 启动仍为开（分支已清）；913 套全量 harness 终态 52 失败逐套甄别零新增红；561=272/0

### tl1746r2 任务面板分布条填充
- 根因=轨道/填充用 span（行内元素宽高不生效）偏离 ResourcePanel div 契约——改回块级+scoped 渐变对齐网页版（轨道/填充/计数三元素齐全）；harness +9 断言钉死

## 二、新功能

### tl1539①段 扫码区块自定义地址（GC tunnel 联调前置）
- LocalServerPage 扫码区块新增「自定义地址」折叠入口（默认 lan 逐字不变）：填 tunnel 域名后 QR=`<base>?token=<token>`；config 键 `serve_connect_base_override` 持久化
- **唯一 token 编码点契约**（encodeURIComponent 源码恰 1 处+两构造器共享 helper，断言钉死）；serve-qr 40/40

### tl1721 聊天-详情群管理面
- **消息清空**：共享历史软删除墓碑（cleared_at）+RestoreHistory 恢复（owner-only 幂等「怎么退」）；已投递邮箱不追回、queued 翻 cancelled
- **channel_invite 拉人工具**（agent 五件套）：补 856⑤ 副管理员有权限无载体缺口；正反例权限钉（副管理员可/普通成员拒/人类不能被 AI 拉）

### tl1718 工具组默认折叠
- 实验室→界面→视图布局新增开关（推荐档，默认关）：开=宽窗口也折叠为单图标（复用窄宽折叠形态）；关=与引入前逐字节一致

## 三、可靠性/修复

### tl1765 停止失效+活跃态分叉（根因定性翻案）
- **僵死的是前端活跃态非引擎 turn**：rewrite-conflict recovery fork 切会话身份吞 turn_done——本地 reducer 卡 running、watchdog 投影 known 时整体禁用=分叉 2 小时无收敛
- 修一=分叉态停止按钮可达（三档语义零改动）；修二=watchdog 禁用条件收窄双通道一致（分叉保持武装 30s 探针收敛）

### tl1763 Shell 检测（根因翻案）
- 「Git Bash 未检测到」真因=**probeBash 3 秒预算 vs msys 冷启动 2.4~3.7s 误杀**（三路推导全在工作）——预算放宽 10s（健康机秒过不变慢）
- pwsh Store 版别名（WindowsApps）入候选（MSI>Store>内置 5.1）；label changed 告警根除

### tl1762 edit_file 证据门 source_token 契约断裂（P1）
- 真断裂=生产态批收尾器重建 read_file 结果时**抹掉 citation**（模型永远拿不到取货码）——挂起-终局应用修复+ParseReadTrailer 剥离兼容+recovery 文案「同轮 read 不算证据」指引；端到端红转绿+伪造 token 保持被拒

### tl1751 压缩读数攒批解耦
- StreamAuxiliary 旁路 onProgress（append 前实时喂 meter）——压缩卡实时 tokens/tps 恢复真实；hover 16838 假值（排空速率）机制上不再发生；重试原子性零改动（既有钉+新钉双证）

### tl1752 上下文读数实测化
- 「实测/估算」标签与读数同源收口（标签改看锚 ok 布尔）——重启后未撞超限即实测、压缩后 1 轮内翻实测、冷启动正确估算；旧宿主兼容零变化

### tl1743 Go 工具链 go1.26.9→go1.26.11（安全）
- 修 GO-2026-6859（net/http MaxBytesReader 竞态 panic）；govulncheck 双模块零漏洞；跨工具链归因矩阵证零引入

## 四、治理/归一化

### tl1750 预存红最小档（12 支清零，生产代码 diff=0）——含在包108 后延续批次
### tl1760 出包链祖先性校验自动化
- verify-release-ancestry.mjs（cherry patch-id 主+四判例矩阵）+fork-release.sh 建包前门+workflow job——包80/包98/tl1724 三历史事故重放全拒；**下个出包起自动防虚报**
### norm-567 归一化#5+#6
- 上下文三入口单源收敛（斜杠/serve/面板同读权威账+零阈值恒真老毛病修）+worktree 合并运行时占用检查下沉 MergeBack（两入口自动同享）
### ui-data 界面数据源四件（1729/1730/1731/1732）
- 成本 props 单源/cacheHit B1 方案唯一发射点+分歧告警/plan 配额锁死防回漂（盘点误报判定）/TaskMonitor 收敛 catalog
### tl1685 EBDD 监测扩面四项
- 首响 provider 分层（当期发现 deepseek 尾部差 mimo 近一倍）/boot 三相独立门/slow switch stage 细分/effort 比值化（第 6 期误判形态消除）；产品代码零改动
### tl1754 死码清理批量件
- 20261110 评审 12 处死码+C1 74 行死方法+serve 三份同构+收敛组——**净减 1398 行**；候拍板 7 项（无法判定项）不动纪律执行
### tl1745 compact 聚焦词引导
- 斜杠菜单三语「压缩上下文 [聚焦词]」+输入过程 hint 行；压缩行为零改动

## 五、装机验证清单（交 reasonix 开发线）

1. tl1741r2：实验室 8 卡可见无主开关+子项可读写+fullAccess 唯一主卡
2. tl1746r2：任务面板三张分布卡彩色填充条（对齐网页版）
3. tl1539：serve 面板展开自定义地址→填 tunnel 域名→QR 切换→GC 扫码连接（联调）
4. tl1721：群详情清空（两击武装）→恢复可用；AI 拉人权限正反例
5. tl1765：停止三档在异常态可用；无 label changed 告警误报
6. tl1763：设置→沙箱 Git Bash/pwsh 7 均检出
7. tl1751：压缩过程实时 tokens/tps；hover 吞吐百级真值
8. tl1752：重启后上下文卡直接「实测」
9. tl1762：edit_file 证据门不再死锁（read_file 尾部带 source_token）
10. tl1743：go1.26.11（govulncheck 零漏洞）
11. tl1718：实验室开关「工具组默认折叠」两态
12. tl1750：预存红清单较包108 缩短
13. tl1713 低收益门：装机观察复压间隔（已拍板默认值）

## 六、候拍板移交

- tl1754 候拍板 7 项（无法判定不动清单）+desktop 15 环境敏感预存红候派单（tl1763/tl1766 基线复现同族并入）+包级 10m 超时（建议 30m）+tl1746 二期+tl1733 Block2（12 月初打点成熟）+tl1730 Go 发射点手术+tl1735 B2-4 维持例外
- 预存红全族：包110 前端全量基线对照零新增（1741r2 913 套/1754 113/114 唯一失败基线同红）
