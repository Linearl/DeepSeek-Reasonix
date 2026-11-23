# 包111 release notes — v1.38.3-20261123-1518

> 基线：包110（v1.38.3-20261122-1457，构建点 7fa4d346c3）
> 构建点：`520cf8bb08`（main-v2-stable，含棘轮断言修正 commit）
> 产物：`desktop/build/bin/reasonix-desktop-amd64-installer.exe`（74,077,123 B）
> 执行线：zcode 开发线（本包 20 支 merge 全量 zcode 滚合，含合并线先收编的 6 支转含）

## 一、返修（两项）

### tl1741r2 实验室「卡体保留、主行撤」+尾项去置灰
- 用户裁定纠正（清主选项≠砍整卡）：8 张 stable 卡**回归 rail（无主开关）**，子项全恢复可配置（自动驾驶问询超时/五类授权/轮次上限、sessionCollab 十项、资源治理整卡等）；**尾项=常开卡行去置灰**（alwaysOn 标记四卡打标，rail 行观感与有开关卡一致，fullAccess 唯一真开关不变）
- config 手写主键 false 启动仍为开；913 套全量 harness 终态 52 失败逐套甄别零新增红；561=279/0
### tl1746r2 任务面板分布条填充
- 根因=轨道/填充 span 块级语义偏离（ResourcePanel div 契约）——改回 div+scoped 渐变对齐网页版；harness +9 断言

## 二、新功能

### tl1767 视频输入接入
- 模型能力双门控（MODEL×ADAPTER）+video block wire 投影（zhipu video_url 唯实证协议）+不支持转文本 fail-closed；设置页视频勾选解禁（能力探测驱动）；图片/音频字节级零回退
### tl1539①段 扫码区块自定义地址（GC tunnel 联调前置）
- 「自定义地址」折叠入口（默认 lan 逐字不变）+`serve_connect_base_override` 持久化+唯一 token 编码点契约；serve-qr 40/40
### tl1721 聊天-详情群管理面
- 频道历史**软删除墓碑+恢复**（owner-only 幂等「怎么退」）+channel_invite 拉人工具（856⑤ 副管理员载体补齐，正反例权限钉）
### tl1772 decision-patterns 注入分层（A 方案）
- 索引常驻 1.6KB（**省 83.2%**）+全文按需硬指令；解析失败静默回退全文永不注空块；开关关逐字节一致；连带 memoryIndexSlim 移组安全/成本控制
### tl1718 工具组默认折叠（推荐档默认关）
### tl1745 compact 聚焦词引导（三语菜单+ride-along hint）

## 三、可靠性/修复

### tl1765 停止失效+活跃态分叉（根因定性翻案）
- 僵死的是前端活跃态非引擎 turn（rewrite fork 吞 turn_done）——修一=分叉态停止按钮可达（三档语义零改动）；修二=watchdog 禁用条件收窄双通道一致（30s 探针收敛）
### tl1739r2 epoch-fence 重复渲染复发返修
- 渲染入口按 turn id 去重（reconcile 重放不重复 append）——复发机制链坐实（fork 吞 turn_done 后账本归零水位线重灌）；切 tab 自愈不变
### tl1762 edit_file 证据门 source_token 契约断裂（P1）
- 真断裂=批收尾器抹 citation——挂起-终局应用+trailer 剥离+recovery 文案；端到端红转绿+伪造 token 保持被拒
### tl1763 Shell 检测（根因翻案）
- probeBash 3s 预算 vs msys 冷启动 2.4~3.7s 误杀——放宽 10s（健康机秒过）；pwsh Store 别名入候选；label changed 告警根除
### tl1771 Shell 探测跨重启持久化（tl1763 续作）
- 探测成功落盘（machine/user 指纹隔离防网盘污染）→启动缓存先行验证→失败自然淘汰回退全量——**只变好不会变坏**；cache-hit 日志+删 bash 无假阳性双载体实测
### tl1768 信件批量清理两段式
- mailbox 第四动作 batch_dispose（dry_run 缺省 true=忘写 flag 永不误删；archive 可恢复/delete 物理移除 tl1722 双语义；硬顶 500 越顶拒给切片指引）+UI 多选批量/跨页全选；两路径同源底层

## 四、治理/基建

### tl1760 出包链祖先性校验自动化
- cherry patch-id 为主判定矩阵（包80/包98/tl1724 三历史事故重放全拒）+fork-release.sh 建包前门+workflow job——**下个出包起自动防虚报**
### tl1756 门禁元数据退役机制
- tsc 226 条+fork-integrity 847 条（实测口径）配退役机制+盘点脚本（首轮抽样：tsc 10 对 stale 候选+5 锚红 4 换代 1 退役候选）——**候拍板只列不删**；机制发现：562 计数锚假告警第三次复发候升档 checklist
### tl1754 死码清理批量件
- 20261110 评审 12 处死码+C1 74 行死方法+serve 三份同构+收敛组——**净减 1398 行**；候拍板 7 项不动纪律执行
### tl1685 EBDD 监测扩面四项
- 首响 provider 分层（当期发现 deepseek 尾部差 mimo 近一倍）/boot 三相独立门/slow switch stage 细分/effort 比值化（第 6 期误判消除）；产品代码零改动
### norm-567 归一化#5+#6
- 上下文三入口单源收敛+零阈值恒真老毛病修+worktree 合并运行时占用检查下沉（两入口自动同享）
### ui-data 界面数据源四件（1729-1732）
- 成本 props 单源/cacheHit B1 方案唯一发射点+分歧告警/plan 配额锁死防回漂/TaskMonitor 收敛 catalog
### tl1770 压缩高残留三问分解（诊断件）
- 641K 构成表（摘要滚雪球证伪/图片底座证伪=tl1656 口径无需动）+涨幅归因（读数对象切换，真凶=save/heal 域清空 wire 内容——**数据完整性问题候单独开件**）+7.6% 折叠率三因定性（非判定过窄）
### tl1769 hover 完整对话名（小修）
- 悬停第一行恒完整对话名（anywhere 换行）；运行圆点不出现在 tooltip

## 五、装机验证清单（reasonix 开发线逐项）

1. tl1741r2：实验室 8 卡可见无主开关**行观感正常不置灰**、子项可读写、fullAccess 唯一主卡
2. tl1746r2：任务面板三分布卡彩色填充（对齐网页版）
3. tl1539：serve 面板自定义地址→tunnel QR→GC 扫码连接（**联调**）
4. tl1721：群详情清空/恢复；AI channel_invite 正反例
5. tl1765：异常态停止三档可用；无 label changed 误报
6. tl1763+tl1771：Shell 检测 Git Bash/pwsh 7 均检出；**重启后二次启动日志见 cache-hit**
7. tl1751：压缩过程实时 tokens/tps；hover 吞吐百级
8. tl1752：重启后上下文卡直接「实测」
9. tl1762：edit_file 证据门不死锁（read_file 尾部 source_token）
10. tl1767：**真机 glm-5.3-flash 勾视频拖 mp4 确认可描述**（候装机实测）
11. tl1768：mailbox batch_dispose 两段式+UI 多选批量
12. tl1772：实验室「决策模式提取」开关两态（开=索引 1.6KB/关=全量逐字节）
13. tl1769：hover 第一行完整对话名
14. tl1756：盘点脚本 --self-test 绿（门禁本体零改动）
15. tl1713 低收益门装机观察（默认值已拍板）

## 六、候拍板/移交

- tl1770 甲案（save/heal 域数据完整性**建议单独开件**）+乙案（tl1651 migrate 门受控通道小改）——候立项
- desktop 15 环境敏感预存红候派单（tl1763/tl1766/tl1772 基线复现同族并入）；包级 10m 超时（建议 30m）
- tl1754 候拍板 7 项+tl1756 首轮退役候选（4 换代+1 退役）候拍板；562 计数锚假告警第三次复发候升档 checklist
- tl1746 二期+tl1733 Block2（12 月初）+tl1735 B2-4 维持例外+tl1730 Go 发射点手术——候拍板
- tl1103 ledger 未跟 tl1711（tl1769 顺带发现）候派单

## 七、判例沉淀

- 棘轮替换防坑：**数字常量带下划线格式（1_467.3）替换需按断言行精确匹配**（首笔命中注释未中断言，二笔修正）
- 删实现必跑断言已删项既有测试+**回实现同样跟测**（tl1741 两轮打回同族）
- 中途读数不可作全量结论（harness 终态落日志）
- 合并后 wails generate+tsc 必跑；合并撞面并集解（数字断言跑实测定稿勿手算）
