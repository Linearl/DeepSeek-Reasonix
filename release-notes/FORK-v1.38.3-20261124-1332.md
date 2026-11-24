# 包113 release notes — v1.38.3-20261124-1332

> 基线：包112（v1.38.3-20261124-0820，构建点 89a123dae6——**其 notes 已勘误**：tl1786/1788/1789/1767/1769/1774/1777 一期/1779/1780 十项虚报，实际全部随本包入库）
> 构建点：`2c26ef1ad8`（main-v2-stable）
> 产物：`desktop/build/bin/reasonix-desktop-amd64-installer.exe`
> 执行线：zcode 开发线（16 支 merge 全量本包，zcode 滚合）
> <!-- claims: feat-tl1788,feat-tl1789,feat-tl1786,feat-tl1767,feat-tl1772,feat-tl1777,feat-tl1769,feat-tl1766,feat-tl1756,feat-tl1771,feat-tl1768,feat-tl1774,feat-tl1779,feat-tl1780,feat-tl1787,feat-tl1742 -->

## 一、背景：包111/112 虚报事故修复（本包主体=漏合补滚 14 支+新交付 2 支）

用户令全分支扫描发现包111 虚报 6 支+包112 虚报 10 项（tl1760 判例第 4 例）：notes 预写在飞支+滚合动作缺失+tl1760 门只挂 fork-release.sh 未接实际出包路径。已全部补滚+两包 notes 勘误+**防线接线**（verify-release-ancestry.mjs `--notes` claims 行 SSOT+build-local-installer.sh `--verify-notes/--verify-baseline` 建包前门非零即中止——**本包起强制生效**，本 notes 的 claims 行即首例）。

## 二、新功能

### tl1742 provider content_filter 拦截上下文自动收集（用户 20261121 需求，20261121 交付本包入库）
- 被拒轮 A/B/C/D 四段+元数据（provider/model/finish_reason/时间戳）自动旁路落盘 `%APPDATA%/reasonix/rejection-cases/rej-*.json`（0700/0600 原子写）；收集失败不阻塞恢复阶梯；零依赖汇总脚本 `scripts/rejection_cases_report.py`（清单+特征摘要+改写对照）
- 6 钉验收矩阵全绿（rewrite/degrade/exhausted/失败不阻塞/零拦截/工具轮重置六场景）
### tl1767 视频输入接入
- 模型能力双门控（MODEL×ADAPTER）+video block wire 投影（zhipu video_url 唯实证协议）+不支持转文本 fail-closed；设置页视频勾选解禁；图片/音频字节级零回退
### tl1787 版本管理三件续作：pre-repair 副本自动拼接（自动挡）
- CheckPreRepairSplice 集合级判据（共享头+分叉尾独有前缀恰为空/长链精确前缀回退轮边界/lineage 锚点+时间线）+SessionSpliceLedger 档案袋（.splices.json 入 sidecar 清单 SSOT，同 backupDir 幂等防重试风暴）+校验值本地零额度重算（主线 ContentDigest/Turns/Revision 原值保留）
- 触发=events_idle_sweep tick 首行（先于旋钮门，两机制正交）；开关 `experimental_prepair_autosplice` **默认关**（实验室「运行可观测」卡）；每 tick 上限 4、busy 让路、失败只记日志不阻塞 sweep；面板行「已登记拼接」标注；投影零改动字节级钉 5/5
### tl1772r2 决策模式注入分层开关归组（返修）
- 移 safetyCostControl 卡 memoryIndexSlim 行后邻位+两态语义三语文案+「决策模式」前缀防与记忆注入瘦身混淆；配置键/档位/setter 零变化；撞 tl1741r2 lab 布局并集解（562 断言四数换代实测全过）

## 三、可靠性/修复

### tl1786 崩溃归因+黑匣子三件（P1）
- 归因结论：exit=1 三通道零痕迹+仓内所有 exit=1 路径都先写日志 ⇒ 唯一吻合=外部 TerminateProcess 类击杀
- 黑匣子：launcher last-abnormal-exit.json 富化五字段（reason/stack/lifecyclePhase/wedgePoint/crashFatalSeen）+desktop decoder 锁步+webview2 插桩附调用栈——下次无声退出死亡瞬间写证据
### tl1777r2 输入框溢出复发返修（一期+返修均随本包）
- 根因=手势直写与 React style diff 基线分裂（松手 logicalHeight 同值跳写 DOM，var 停手势残值 160 vs textarea 302 穿墙 151px）——写手落定 useLayoutEffect 强制对齐绕 diff，拖动 liveResize 独占保留；新钉 8/8+tl1578 24/24 等七钉全绿
### tl1769 hover 完整对话名（一期+渲染级证据钉均随本包）
- classic 卡 title 改对话名优先+preview 降级独立补充行+workbench tooltip 两行结构+anywhere 断词不裁剪；渲染级钉 16/0+7/0+一期 28/0
### tl1771 Shell 探测跨重启持久化
- 探测成功落盘（machine/user 指纹隔离防网盘污染）→启动缓存先行验证→失败自然淘汰回退全量——只变好不会变坏；cache-hit 日志
### tl1774 provider base_url 协议路径校验与提示（issue #105）
- 裸 base 保存触发示警+「补全」一键改写+404 错误文案附指引；契约层自动补全不回退钉

## 四、治理/基建

### tl1788 环境误报治理框架
- 统一 tsx 测试入口（css/svg stub+扩展名自动纠偏）/harness 失败单跑复验甄别（ENV-FLAKY 不阻断、再红阻断）+jsdom 预飞/go-test-triage.sh 失败隔离 3 次/Makefile go test 超时 10m→30m
### tl1789 基线红治理（结构性收获；阶段二经核验=14 项已全修，零代码收口）
- desktop 包从一跑就崩治到全绿（4564 支=4514 PASS+50 SKIP+0 FAIL 终态）；32 修绿+12 挂牌隔离+55 族归档候拍板；tl1652 manifest 欠账修复；B 类 9 支 crash 上传测试真根因=tl1665 政策门硬编码 false（非环境），门控 SKIP 候拍板挂 tl1665
### tl1756 门禁元数据退役机制
- 退役机制+盘点脚本+首轮台账（tsc 深验 10 对 stale 候选候拍板，只列不删）
### tl1779 上游继承兼容层删除
- C5-C28 十二处死码+3 配套测试+transport_sse.go 整文件，净减 286 行；C14 两案候拍板
### tl1780 同构合并压平五组
- E3 频道读双胞胎/E4 bot 渠道镜像/S1 四级链压平/T1 sink 双胞胎合并，净-33 行
### tl1766 回归判例
- deferred bridge 钉桩「读取在途」窗口 25 断言
### 出包链防线接线+两包勘误（治理 commit）
- verify-release-ancestry.mjs `--notes`+build-local-installer.sh 门参数；包111/112 notes 勘误 commit

## 五、装机验证清单（reasonix 开发线逐项）

1. tl1742：真机触发一次 content_filter → `%APPDATA%/reasonix/rejection-cases/` 出现 rej-*.json 含 A/B/C+元数据；`python scripts/rejection_cases_report.py` 出汇总
2. tl1767：真机 glm-5.3-flash 勾视频拖 mp4 可描述（不支持模型转文本不报错）
3. tl1787：开实验室「运行可观测」卡 `experimental_prepair_autosplice` → idle 后面板行出现「已登记拼接」；关=零动作
4. tl1772r2：实验室「安全/成本控制」卡见「决策模式注入分层」开关（两态三语，hint 注明与记忆注入瘦身互异）
5. tl1786：异常退出后 last-abnormal-exit.json 含 reason/stack
6. tl1777r2：拖拽调小输入卡+长草稿→松手不溢出卡外
7. tl1769：hover 会话第一行恒完整对话名（长名不裁剪）
8. tl1771：重启后二次启动日志见 shell probe cache-hit
9. tl1774：设置页填裸 base（无协议后缀）保存→示警+「补全」按钮
10. tl1768：信件多选批量清理（dry_run 缺省 true；archive 可恢复）
11. tl1756：`node scripts/gate-metadata-audit.mjs --self-test` 绿
12. tl1783/1784（包112 转验）：哨兵 CONFIG.TOML 变体拦截+反风暴闸东八区 00:30 计新日
13. tl1785（包112 转验）：装机重启投影降级保留（不再 2M 全量）
14. tl1713 低收益门装机观察（已拍板默认值）

## 六、候拍板/移交

- tl1742 首例案例格式回填（用户 20261121 case-001 样板）装机验证后归档
- tl1789 阶段二定性（verified/cancelled）tl 回写；TempDir 清理竞态两支（EnsureBlankTab/ParkReplay）候并入 C 类
- tl1665 遥测门：B 类 9 支 SKIP 复活条件挂靠，候拍板
- tl1770 甲案（save/heal 数据完整性单独开件）+乙案（tl1651 migrate 门与 tl1785 三闸对表）
- tl1756 首轮退役候选（4 换代+1 退役）+tl1754 七项候拍板
- tl1787 两候拍板：lineage 拆件（备份派生分支枚举进版本对话框）/自动路径选旧链 HEAD 新语义
- tl1779 C14 两案；包级 desktop 全量 45m 固化或拆包
- GOCACHE 并发写损坏判例（WMI 并行 go test 实锤）——包级测试建议串行/错峰，候纪律化
