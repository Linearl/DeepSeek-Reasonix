# Release Notes — FORK v1.38.3-20261106-0755（包 79）

> 基线：上一时间戳包 v1.38.3-20261105-2327（包 78）。本包 = 昨夜打回三件返修 + 版本面板二轮定稿 + 群聊 tab 形态 + 工作流可观测三件 + 出包门禁清红。
> 主线 tip：bc6d376c5（含协调线两笔出包清障）。构建：2026-11-06 07:55，EXIT=0，STAGED 四件已铺。

## 打回返修（昨夜用户实测三件，全闭环）

- **切换主线生效位三环返修**（tl1430b，P1）：环③根因=damaged fork 只写 flag 不写 digest 致 RecoveryPreferenceCurrent 拒认（回执成谎言的调研员1 翻车形态）——damaged fork 按 tolerant 重放写 digest+去掉一票否决；环② Selected 恒 isMain 双钉改 meta.RecoveryPreferred 单一真相源+前端主线行跟随；head_switch 前置物化防御。Go 端到端 7 案绿。
- **归档返修三缺陷**（tl1429b，P1）：①正常路径 self_lock 豁免——SweepOrphan 锁仲裁失败查登记表，本进程残留直接撤+heal 自愈（不再要求升级 force），真外部不越界；②回收站双记录根堵——cleanup-pending 无桶不建新桶+meta 缺失桶放行，清空不再报 outside dirs；③侧栏残留——deleteTopic 并发元数据锁当轮同步重试。新钉 6 案全绿。
- **头部入口改名数秒同步返修**（tl1413b，P2）：机制反转定案——头部链确实经 RenameTopic 且 bounded 推生效，漏接在 UI 消费端（ProjectTree.refresh 空 options 不重拉）——App 层 pushTopicTitlePatch（单调 seq）+ProjectTree 乐观补画。16/16 钉+46 文件回归绿。

## 版本面板二轮定稿（用户 demo v5 逐版确认落地）

- **tl1415b 面板重构**（P2）：全页一张表+类型列四色（sticky+背景）／候选链并入副本行／两组折叠+计数徽章（待处理展开、已合入折叠）／三步流程按钮带步骤号+hintline／预览=尾部消息（徽章+结尾优先弹窗）／行最小功能集／沉底横排+更多▾／**新按钮「清理非主线副本」**（二次确认、孤立副本保护）／全按钮 hover。tl1415 重写 65 断言+组件族 17 套复跑全绿。

## 群聊形态（两段口径落地）

- **tl1441 群聊 tab+Composer 群聊模式+能力六件**（P2）：①协作中心「打开标签页」→channelTabStore 伪实体进 tab 条，激活态 ChannelWorkspacePanel（顶部栏返回/群名/成员/详情管理）——群聊=会话 tab 同型实体；②Composer 结构性隐藏会话专属槽位（模型/effort/权限等=零展示门控），发送桥接 channel_send，draft 按 channel:<ref> 分键；③能力六件=粘图/截图（落全局工作区根 attachments，读桥 scope 守卫）/表情/改写草稿（借辅助模型通道）/历史提示词/快捷指令，消息流附件缩略图（成员侧可见图）。harness 133 断言全绿。

## 工作流可观测与引擎（五件）

- **tl1443 recovery fork 全链路打点**（P1，纯观测零行为变化）：每决策出口一行结构化 WARN（五枚举/十五级/24 写路径/disk_writer 归因等）+doctor 全工作区分叉普查段（历史 81 次分叉立即可用）。
- **tl1433 工作流运行可感知**（P2）：run 行并入「任务」面板+点击跳转画布定位高亮（workflowRunFeed 喂单零新机制）。
- **tl1434 节点详情「工作过程」只读回放**（P2）：逐次执行入口+只读转录形态（零写入铁律三案）。
- **tl1435 右侧栏收工作流子代理**（P2）：运行中区两段数据源同源化+5s 轮询补盲区（557 同族）。
- **tl1432 工作流 typed result 容错提取+定向重发**（P2）：三级解析管线（整体→首末切片→平衡扫描，截断永不抢救）+JSON-only 指令重发预算可配；错误双轨三语。
- **tl1438 use_capability inspect 双层返回**（P2）：机器 schema（Schema() 原样字节零手写）+prose+参数表+指纹；前端 ToolCard 可折叠详情。

## 其他

- **tl1437 产物列表交互补齐**（P2）：选中态/右键/复制路径/默认应用打开/定位/添加引用六项。
- **tl1424b 预存红清理第二轮**（四项）：task562 追认豁免、transcript-viewport 真产品缺陷修复（排水规则方向对称）、typography 名单修正、subagent-progress 测试假设修。
- **出包门禁清红（协调线代修两笔）**：check:css 五零定义类+硬编码色+z-index fallback（e59f6437c）；JS gzip 棘轮 622.6→629.1 实测+1.0（bc6d376c5）。

## 已知边界

- 合成保留会话行不沉底（两引擎两条确认链防路由歧义，候用户追认）；旧「清理多余副本」钮由「清理非主线副本」替换。
- 包78 遗留观察：启动 leg_errors=1（非阻断）；tl1416 切tab、tl1410 状态同步观察项。
