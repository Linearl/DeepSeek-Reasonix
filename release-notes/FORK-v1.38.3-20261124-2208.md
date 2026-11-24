# 包114 release notes — v1.38.3-20261124-2208

> 基线：包113（v1.38.3-20261124-1332，构建点 `4a46bce041`）
> 构建点：本 notes commit 后 tip（棘轮六线实测一次抬齐 commit 随）
> 产物：`desktop/build/bin/reasonix-desktop-amd64-installer.exe`
> 执行线：zcode 开发线（14 支 merge 全量本包，zcode 滚合；同日编队 1792→1674b）
> <!-- claims: feat-tl1792,feat-tl1768r2,feat-tl1767r3,feat-tl1758,feat-tl1775,feat-tl1776b,feat-tl1744b,feat-tl1794,feat-tl1791,feat-tl1793,feat-tl1797,feat-tl1795,feat-tl-prered,feat-tl1674b -->

## 一、安全/正确性（P1 主力）

### tl1795 autopilot 裸旗脱节三层失步根治（P1）
- 切 Yolo 后 ask 仍按 autopilot 15s 自决——主断点=**活控制器构造期姿态全仓无活更新口**（切档残留 unattended→无需重启即 armed 自决；协调线定位的「重启窗口脱节」经验证为次断点）；另实证 goal-state sidecar OR 语义复活腿
- 四件修法：reconcile（entry 胜 sidecar+`[autopilot-restore]` 锚日志+notice）/fail-fast（SetApprovalTierForTab 返错、前端回滚乐观旗+双轨 toast）/七入口审计（legacy SetModeForTab 同款暗道补堵）/回归（control 4+desktop 5）
### tl1793 版本管理名字+空壳双根因（P1）
- 面板内部名=recoveryDisplayName 回退链漏两级——补齐 `custom_title→topic_title→titles sidecar→stem` 与侧栏同序
- **归档空壳数据实未丢**：真凶=归档后按 topic 打开静默新建 0 字节空会话冒充原位（真机取证实锤）——新建分支前置归档态闸拒绝，明确告知「在回收站请先还原」

## 二、新功能

### tl1744 右上角「?」帮助菜单五项
- 产品文档/问题上报/提需求（新会话+composer 预填 `/gh-issue-submit`，仓库 slug 单源）/导出日志/关于·版本；三语齐；composer 残留清场防重复灌注
### tl1797 对话 tab 悬停卡片三区块
- 身份区（完整标题/项目/最后活跃）+运行区（运行态/已耗时/模型努力档/模式态）+消耗区（上下文占用/距压缩阈值/会话成本/总轮次）；400ms 延迟弹出+pointer-events:none 物理不遮挡；**4 项不可得字段如实申报不硬造**（数据源核对表进报告）
### tl1674b pre-repair 手动合并入口（用户拍板转正）
- 「拼接登记」两段交互（链预览确认→元信息登记）与 1787 自动自愈共用同一判据与档案袋，source=manual 可观测区分；**转录/events 逐字节不变**字节级钉 PASS（纯元信息铁律）；健康闸提示替代硬拦（1787 遗留落地）
### tl1775 任务面板「从浏览器打开」
- tl.py export 静态看板一键系统浏览器直开；stale（html mtime 早于 db）提示不拦截；机制发现：wails BrowserOpenURL 拒 file:// 须走 shell 关联
### tl1794 模型列表 badge 补视频/音频输入
- Picker 直消费 tl1767/1008 三态助手，与附件链门控同源零新字段（逐条镜像后端 resolver 核验）；图/视/音三 badge 并排

## 三、顽疾根治

### tl1776b 贴底回弹根因修复
- 根因=tl1699 采样器流式盲区（echo scroll 时下一 chunk 已挂载→atBottom=false 稳态污染→宽度事件引爆 intent 降级停跟）——修法=尾底见证（writer lastTailFloor：坐记号上=内容长高非用户滚动）；新套件 16/0 红转绿+同族 15 套件零回退；顺手修同源预存红 tl1204（30/30）
### tl1767r3 视频附件静默丢弃缺口
- 门控关选视频从「四路分发全不收零提示」→warn toast 含 model_overrides 开启指引；videoTooLarge 门关态改报不支持（语义取舍注明）；门控开字节级不变；新钉 19/19
### tl1768r2 协作中心遮挡确认弹窗
- confirm 家族 1300→`--z-popover-elevated`(1303) 一条规则修复（任务 984 同构先例+外点关闭规则语义佐证），避开 modal 全抬全局重排；顺带治好会话接管等程序化弹窗同遮挡
### tl1791 实验室 CDP 稳定锚点+深链
- 组头 lab-group-*8/rail 卡行 lab-entry-*28/开关行 lab-toggle-*70 全量覆盖，**门禁测试漏一即红**（yaml 新增自动要求补锚点）；`#settings/lab/<卡>` 单跳直达（关闭沿清 hash 防脏态）；CDP 真浏览器 bench 8/8
### tl1796 autopilot 选项消失根因
- 「切 yolo 消失」为时序错觉——实为 tl1741 撤主键连带删写点与读源，前端死读恒 undefined；补全新 opt-in 写点链（`experimental_autopilot_composer` 默认关=关态逐字节不变），退役语义零回潮

## 四、治理/底座

### 预存红集中清零 9 项全销（用户令：发现预存红需处理）
- bang 两支真根因=jsdom 无布局引擎虚拟化行不挂载（补高度桩治本，非 attachEvent 噪声）；tl1092 夹具闭括号还账（前案确诊）；task649 跟 tl833 token 迁移；Go 三支裸 timeout 5s→30s+TempDir 清理重试（tl1789 同款药方）；desktop 600s 超时甄别=**纯预算非产品泄漏**（三族 goroutine 逐族核过）；desktop 全包 1212s EXIT=0+control 404s EXIT=0，零产品代码改动
### tl1758 formatTime×5 散落合并单源化
- messageTimestamp 增两导出+5 面别名接线+改名消歧+门禁钉 9 项（植入即红）；Notifications epoch 戳边界收敛申报制
### tl1792 配置问题横幅「复制错误信息」
- 完整错误串（路径+toml 行号原文）一键进剪贴板（后端本就完整，纯前端接剪贴板出口）
### 姊妹断言欠账补同步 ×4
- tl1746/task1005（计数 4→7）+task895（成员清单 3→5）——tl1787/1772r2 并集解时漏同步，判例固化：计数/结构换代必 grep 全 harness

## 五、装机验证清单（reasonix 开发线逐项）

1. tl1795：autopilot 切 Yolo→发 ask 面板持续阻塞不自决（15s 后仍在）；两端旗一致
2. tl1793：版本管理行显示中文会话名（内部编号降为次要）；归档后打开原对话→提示在回收站而非空壳
3. tl1796：开实验室新开关 `experimental_autopilot_composer`→模式下拉含 autopilot→切 yolo 关 tab 重开仍在；总闸 off 隐藏
4. tl1674b：版本管理 pre-repair 行「拼接登记」→链预览→确认登记（对话内容零变化）；面板见「手动」徽标
5. tl1744：右上角「?」五项菜单；问题上报/提需求点开新会话带 `/gh-issue-submit` 预填
6. tl1797：hover 对话 tab 半秒→三区块信息卡；长标题换行；运行中耗时每秒走字
7. tl1776b：流式输出贴底→视口跟随不回弹；上翻阅读不被拽
8. tl1767r3：不支持视频的模型添加视频→toast 提示（非静默）
9. tl1794：模型弹窗勾视频/音频→列表行亮「视频输入/音频输入」badge
10. tl1768r2：协作中心信件多选删除→确认弹窗可见可点
11. tl1775：任务面板「浏览器打开」→默认浏览器开看板；stale 提示
12. tl1792：构造 config 错误→横幅「复制错误信息」→剪贴板完整串
13. tl1791：CDP 或浏览器地址栏 `#settings/lab/<卡名>` 直达分组
14. tl1758：界面时间显示与包113 一致（行为零变化，回归确认）

## 六、候拍板/移交

- audio 同机制静默三档修复候选（tl1767r3 申报：最小/中等同批/彻底四路统一）
- tl1796 显隐门常开 vs 开关两案（tl1741「稳定档不再提供开关」边界）
- tl1795 镜像缺陷 follow-up：会话内新参与 autopilot 不重建控制器（安全向，底座已铺）
- restartUpdate 归属漂移（配置归 updateFeedback 界面画在 toolOptimizations 卡）候拍板
- tl1793 遗留三项：孤儿 sidecar 清理/probe 活引用释放/错误文案三语化——候派单
- desktop 包级超时门禁固化（600s=预算甄别结论）/测试面退场补齐/tsc 基线滞后/clientHeight 桩共享化——预存红报告候拍板 4 项
- composer-quick-command-bang 预存红已修（tl1794 申报的同款间歇崩留观项并入观察）
- tl1789 阶段二定性（verified/cancelled）tl 回写候协调线

## 七、判例沉淀（本轮新增）

- 计数/结构换代必 grep 全 harness 逐一换代（三次翻车：tl1746/task1005/task895）
- merge-tree 预演不完全可靠（tl1787 误报零冲突实际有冲突）——直接 merge 就地解
- 大批出包前先本地 vite build 实测棘轮六线一次抬齐（包113 九轮教训转化）
- wails BrowserOpenURL 拒 file://——「打开本地文件」须走 shell 关联
- fresh worktree 三件套：拷 wailsjs/ 生成物（tsc TS2307）/拷 node_modules 或 junction/`.cmd` 须纯 ASCII（GBK 吞 CR）
