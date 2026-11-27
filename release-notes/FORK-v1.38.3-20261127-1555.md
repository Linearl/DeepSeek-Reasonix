# 包118 release notes — v1.38.3-20261127-1555

> 基线：包117（v1.38.3-20261126-2200，构建点 `6a1c4af1ee`）
> 构建点：`222da0ee89`（main-v2-stable；含 tl1831c 收官件+tl1844 单源化+锚换代治理+tl1834 CSS 热修 222da0ee89，三轮构建 rc=0）
> 执行线：zcode 开发线（十三支全量本包；本包含 0858 投影事故根因链收口+变更同步四路调研首批落地）
> <!-- claims: feat-tl1828b,feat-tl1827b,feat-tl1830,feat-tl1823b,feat-tl1776c,feat-tl1817b,feat-tl1829,feat-tl-caseC,feat-tl1834,feat-tl1836,feat-tl1814x,feat-tl1844,feat-tl1831c -->

## 一、P1 修复/机制

### tl1831c 投影/转录协同两阶段修复（0858 hash_mismatch 根因链收口，用户拍板件）
- **阶段②治标**：stale keep 双失效修复——structural gate 撤 `len==covered_count` 死等式（2360≠2371 直接死因）改逐条比对；role 改读 sanitize 管线真槽位（ID 标记孪生重跑管线建映射）；失配点前历史原样保留、失配点起 forced rebuild；refusal 结构化进 drop 日志；diff_role 失真观测面修正；transient-only 容忍明确不做（tl1203 fail-closed 安全钉不松）
- **阶段③治本**：takeover 整文件重写→投影协同信号（SaveRewrite 落盘后双分支：活 fold 当场重发 sidecar／死 writer 封存删除+有序重建）；时序钉=transcript 先落盘、sidecar 只能 FOLLOW
- 验收：0858 真会话探针重放**零 drop**（slots=[2139] roles=[tool]，旧门此算术 nil）+user 槽真篡改仍拒+hermetic 8 支+协同钉 3 支+agent 全包 572.7s（唯一红 TestPendingStateManifest 基线同红 delta=0 申报）
- **历史三联核证**：折叠+副本=同一次 diet 事件两步（先备份后 fold 代码钉死）；分叉=独立 conflict/takeover 链；同根=多 writer 自愈病族

### tl1827b RecoverFailedInstall 启动回滚接线（审计 A3 缺口闭合）
- 两处「启动时回滚」注释承诺兑现（失败标记有人写没人读）；启动链 installRetention 前踢出+三态锚日志+陈旧标记清除+幂等静默；e2e 四钉 4/4

### tl1830 孤儿 spinner 兜底接线（审计改口径：非死码是未接线兜底）
- reapStaleTopicActivityStatus 首个生产调用方（5min tick，清除延迟 10-15 分钟）；超 TTL thinking/streaming 清除+双事件广播；waiting_confirmation/error 不清

### tl1776c 贴底回弹第二层盲区根除（装机再现三轮返修）
- tl1165 平滑跟随 transit 稳态使 floor 见证腿结构性失明——writer 暴露 smoothFollowPending 第二见证腿；开关关逐字节不变；红测试 6 红→绿+扩展族 15 套零回退

### tl1817b 问号间距复核返修
- 首版锚错对象（砸进 ⊞ 让位带重叠 26px）——重修挂 ⊞ 既有让位链，两档重叠清零+族内 gap=8px；app-chrome-tabs 113/113

## 二、新功能/体验

### 案C sessionCollabAutoFold optional→recommended 单键升档
- 9 处联动面全换代（LAB_TIER_COUNTS 推荐51→52可选29→28 总数96不变）；默认关语义不变；预存红 task704 两断言+task758 双树实锤申报
- （注：升档漏跟第 10 处门禁锚由本包治理件修复，tl1844 结构性根除复发）

### tl1834 备份表三件改进+强制删除（用户追加件）
- 表头五列/列对齐（grid 共享列模板+tabular-nums）/三键排序（aria-sort+稳定平手钉）；**pre-repair 强制删除**：删除失败弹窗列明+确认强制（只旁路健康闸；路径闸/回收站语义/物理失败如实拒绝）+仍失败诚实报错；新钉 29+24+Go 4 支

### tl1836 待办面板上一回合展开态并入当回合同一渲染单元
- 根因=两套实现（内联简化渲染器无编号/徽章/分组/计数）；统一到 TodoTree；默认折叠不动；95/0 绿+CSS 三门

## 三、质量/清理

### tl-slop2 F3-14 repair 测试面四层处置
- 层①66 删/层②38 改写 Exact/层③48 逐判（undo 接线救回 1）/层④引用网复核保留；两线合计 32-片 267 全清单零遗漏零重复；2 条 flaky 预存申报
- F3-11/12 死码清理（21fef4a145）：宽松版更新函数族 12/13 删+三零调用导出删（**该删除与 tl1827b 撞面致主线编译红 3h，热修 b1218d4a28 恢复两符号**——判例=已滚接线件引用的符号必活）

### tl1829 themePackIDRe 同源化
- ID 长度上限单源化（正则由常量构造，选 A 结构性 SSOT）；钉测试三路径+突变三连验证

### tl1828b CI 安检门两处自病修复
- compress-floor 带引号 for 迭代假绿改 mapfile 逐行+wails-pin 白名单补两行

## 四、治理/基建

### tl1844 tier 联动面结构性单源化（d2 调研落地）
- LAB_TIER_COUNTS 改 deriveLabTierCounts 活注册表归并；task562 六钉换代自洽+下界棘轮+覆盖集断言（连带 task705/707/tl1746/task704-switch 四件同机制逮出）；fork-integrity 两锚形状化（案C 红的那条不再随数值红）；FORK.md 升档 SOP 必改 5 位
- 突变红绿对照：旧机制假绿漏检（钉中钉机理）→新机制红仅 3 条逐 id 指真源+完整翻全绿零触碰；10/10 覆盖

### tl1814x 裸色值遗留三件防回归基建
- 色卡↔定义块校验三闸（57 检查点；首跑抓 6 真实漂移顺修 2 处+11 处 EXCEPTIONS）；红色六族族谱成文；onboarding 裸色值计数钉 PIN=12 新增即红

### 门禁锚换代（8db487776e）+撞面热修（b1218d4a28）
- fork-integrity task562 计数锚换代（案C 漏跟第 10 处联动，d2 现场实锤 848/849 红后修复）；RecoverFailedInstall/HasPendingUpdate 恢复

## 五、装机验证清单（reasonix 开发线逐项）

1. **tl1831c（重点）**：装机重启后投影不再 hash_mismatch drop（此前 0858/1116/1123 三起同族）；日志无 [update-recover] 类外的新 drop 锚
2. tl1827b：安装失败标记场景启动自动回滚（v1 内容复原+[update-recover] 锚日志）
3. tl1776c：开平滑跟随风控开关+快速流式，贴底不回弹（此前两轮装机未过场景）
4. tl1834：备份表有表头可排序；删除失败弹窗列明+确认后强制删除生效
5. tl1836：待办面板「上一回合」展开有编号/徽章/分组/计数
6. 案C：实验室「自动折叠跨会话消息」徽章为推荐档（默认仍关）
7. tl1830：turn 丢失孤儿 spinner 10-15 分钟自动清除
8. tl1829/1814x/tl1844：无感知项（门禁/钉/文档面）

## 六、候拍板/移交

- tl1833 outline 模式实装立项候拍板（17.5 倍覆盖/解析侧零协议改动）
- 变更同步 A/B/C 三支在飞（tl1841 换位登记/tl1842 桥契约门禁/tl1843 CAS fail-closed）——包119 编队
- tl1840 折叠态弹层锚失效在飞；tl1844 已入本包
- 预存红申报：TestPendingStateManifestCoversRepository（tl1806r2/tl1834 已并支文件违规字段，基线同红）+task704 两断言+task758+tl1603/1658 toast 断言+tl1415e
- d1~d4 调研报告四份在 docs/reasonix/report/（缺口总表已回板，7 项全批立项 A~D）
- tl1821/tl1823 soak/F3-11/12 剩余 C37+C30 回收中
