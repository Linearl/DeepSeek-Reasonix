# 包119 release notes — v1.38.3-20261127-1900

> 基线：包118（v1.38.3-20261127-1555，构建点 `222da0ee89`）
> 构建点：`f7c408fe3b`（main-v2-stable；变更同步四支全落地+outline 实装+弹层锚修复）
> 执行线：zcode 开发线（7 支全量本包；主题=变更同步缺口收口+guardian 幽灵清单收窄+outline 模式）
> <!-- claims: feat-tl1840,feat-tl-slop1,feat-tl1842,feat-tl1841,feat-tl1843,feat-tl1850,feat-tl1833b -->

## 一、变更同步系列（四路调研全批立项落地，调研报告在 docs/reasonix/report/）

### tl1841 会话换位登记链收口（变更同步 A，d1-G1/G2/G3）
- **conflict-preserved.jsonl 补进 SessionSidecarFiles**（静态换位/删除/迁移三链同 commit 生效）——副本转正先归档后换位，relay 截断消息不再被 os.Rename 无归档销毁（tl1748「覆盖不再是销毁」在换位链兑现）
- 备份白名单补 splices/rewritelog/conflict-preserved 三键+guardian 族同步；**包级守卫差集门禁**（注入红/移除绿/豁免卫生，guardian 存量欠账上限锁定）
- replaceSessionAfterCancel 补 InvalidateProjectionIfStale（对齐 rewind，20261116 投影 drop 家族入口闭合）

### tl1842 桥契约门禁三层闭合（变更同步 B，d3-G1/G2/G4 中等档）
- **新门 check-bridge-signatures**：Go App 方法↔wailsjs 生成物↔bridge.ts 三方对齐（975 方法 arity 级，build-tag 约束感知），挂 pretypecheck+build 链——实证旧 tsc 内嵌 drift check 恒 any 恒真，签名段首个真门
- bridge-tags 的 40/91 静默跳过改三层注册表（32 包限定名映射+5 偏差清单+3 载荷登记，未注册未镜像即红）
- bridge.ts 清 7 个 Go 侧退役死契约+Approve 过度声明形参清退+4 interface 补线上既有字段；两种口径统一为「改桥必 regenerate 同步」
- HeapBreakdownPath 运行时坏点挂账 KNOWN_MANUAL_ONLY（修复需 App 方法绑定，候后续 tl）

### tl1843 CAS 基线装配 fail-closed（变更同步 C，d4-A1，rev_lag=6719 根源收口）
- **「账本页空白不许动笔」**：checkSnapshotWrite 对基线装配失败（baseState.ok=false）强制 heal 到 observed disk state 再放行写路径——save.go:793 武装前提缺口/base_revision=0 裸奔结构性收口
- heal 基线不担保所有权（ownsPersistedState 拒+BaselineHealedFromDisk 视图+fork trace 卫语句）；可用性=heal 不可失败/append 首写照常/diverged 照常走 takeover 阶梯
- 归因双侧锚（save 入口 WARN 带 disk_revision/ledger_writer origin+pid——d4 遗留「入口为何从未装配」补观测）
- 边界申报：首写 revisionUnknown 不武装 CAS（首笔落账即武装）；同进程多 controller 与 unleased 写入者族本件不修列单

## 二、guardian 幽灵清单收窄（tl1849 调研+tl1850 收窄件，用户定调承接）

### tl1850 guardian 家族派生清单 22→6
- tl1849 实证：豁免表 11 键**磁盘零实例+写入方代码缺失**——不是历史垃圾，是删除清单构造器对 guardianPath 重跑主会话侧车构造器产生的理论派生面（guardian 机制本身活跃必要，autopilot 唯一审批者）
- 收窄到 6 件实际数据面（转录+游标+meta+events+双 index）；豁免表出表 12 键（11 存量+guardian.repairlog 同证死键）+白名单删 3 审计死键
- 新子集构造器 SessionGuardianSidecarFiles+单测钉（6 键集合等值+14 幽灵键回流即红）；**删除链 stem 兜底不动**（防线上限不降）；/clear 链全量展开保留（无兜底 sweep 的唯一清扫面）

## 三、新功能

### tl1833b read_session_tail outline 模式实装（用户拍板，调研实测同预算覆盖 17.5 倍）
- **mode: full|outline 缺省 full**（向后兼容，省略 mode=历史行为 byte 级零变化）；outline 输出「意图摘要行」序列（工具名+状态+意图）
- 意图三级 ladder：description>关键参数表>字典序 catch-all（91.7% 宽松口径，意图是模型经 tl1613 schema description 已持久化的——解析侧零协议改动）
- 流式 json.Decoder 逐记录（长会话不整载）+rune 截断防劈 UTF-8+512KiB 滚动窗口 newest-first 回走
- **集成实测同预算覆盖 20.0x**（≥10x 达标）；消费方走查=全部消费者是模型读文本、无程序化解析（契约安全）；hub/boot/skill 三处文档补 mode

## 四、返修/清理

### tl1840 折叠态弹层锚点失效修复（用户报障）
- 根因=折叠是纯 CSS display:none（元素不卸载），AnchoredPopover 对全零 rect 只防「元素不存在」不防零 rect→弹层落 (8,8) 左上角
- **AnchoredPopover 新增 fallbackAnchorRef**：主锚不可测时同一套定位逻辑换锚工具组折叠按钮；历史提示词+emoji 两处接线；22 弹层全量矩阵处置（修 2/申报其余）；tl1840 钉 24/24 先红后绿

### tl-slop1 八座前端死桥摘除（slop 批 1 收尾）
- MCP 三桥（AuthorizeAndConnect/AddMCPServer/SetMCPServerTier）+remote 五桥——组件层零真调用四形态复核，Go+interface+mock 三册同删，测试连带改写活桥；**净减 297 行**

## 五、装机验证清单（reasonix 开发线逐项）

1. **tl1843（重点）**：正常使用无感（fail-closed 只拦基线装配失败场景）；如遇保存拒绝+heal WARN 锚日志请回报（观测第二层缺口的数据）
2. tl1841：副本转正后 .pre-repair 归档含 conflict-preserved（此前场景归档缺件）
3. tl1842：无感知项（门禁面）；本地 `pnpm typecheck` 触发签名门
4. tl1833b：跨会话读其他会话尾部试 outline（同预算看约 20 倍轮次，意图行可读）；全量模式照旧
5. tl1840：输入栏折叠后点历史提示词/emoji，弹层锚定折叠按钮不跑左上角
6. tl-slop1：无感知项（死代码摘除）
7. tl1850：无感知项（清单收窄）

## 六、候拍板/移交

- tl1845 P1（主对话流整轮漏画）/tl1837/tl1838 未入本包，候派
- tl1842 两 wire 错位 3 件（snake_case vs camelCase 前端读 undefined）候立后续 tl
- tl1823 app-memory 预 soak 在飞；已知边界：tl1833 outline 截断参数手调值待上线读感微调/zcode sqlite 会话适配器另案/tl1613 description 扩展次高频工具是意图率增量路径
- 预存红 3 件候 known-failures triage；tl 回写归协调线（包118 十三支+包119 七支）
