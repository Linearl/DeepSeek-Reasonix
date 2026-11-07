# Release Notes — FORK v1.38.3-20261108-0302（包 83）

> 基线：v1.38.3-20261107-1132（包 82）。本包 = 包 82 后滚合批（9 支，含 tl1427，初版漏列后补记），**全时段出包装机自主授权内自主出包装机**。
> 构建点：`1409254c1`（含 2 个棘轮清障 commit）。

## 滚合九件（包 82 后累计）

- **tl1471 fullAccess×删除护栏**（用户裁 A）：boundedDeleteTarget 末段加 fullAccess 豁免（rootSet.Unbounded 且非 home 子树放行，恢复 10-15 回归行为）；home 本身/祖先/工作区锚点/文件系统根/解析失败 fail-closed 两态不减；拒绝文案三处同步（不再声称 every approval mode）。
- **tl1415f 首开仍错第三轮返修**：链路枚举定案——闸是单一入口，但「面板 Selected」与「打开闸权威#2」定义分裂（digest 漂移→filing=diverged→首开落降级主线）。修=continuePathForOpen 改调 recoveryAuthorityMainPathForOpen（补权威#3=组内唯一 meta 旗标成员，多旗标 fail-closed）；降级副本行可选中+行内删除（同闸回收语义）。
- **tl1476 rewind 前文件安全预检**：rewindCommit 咽喉点分类 plan.fileSafety 挂统一 ConfirmDialog——全 safe 一屏确认、unsafe 禁用确认钮+原因逐条可见（工作区外/载荷已删/权限不足/状态变化）；coverage 警示沿既有流零变化；22 分类案+29 弹窗案 harness 全绿。
- **tl1477 子代理工具摘要行**：单行摘要（工具名+关键参数）+只读详情 tab（参数/输出/错误三段零输入面）；半截 JSON 不猜默认值（截断=原文逐字+标记），toolCallArgsState 四态诚实分层替代旧 parse 静默吞态。
- **tl1478 模型轨迹查看器补强**：sticky 摘要头/批量展开收起（含持久组头返修）、开放式类目过滤菜单（promptEnhance 扩展位已留）、千条级性能钉（1760 条投影 3.6ms）。
- **tl1474 回收站加载太慢**（验收<1s）：瓶颈实测=coverage 证明层 14s/25 桶主导（整转录重放零缓存）。修=ListSessionsMetadataOnly（永不重放转录）+coverage 备忘 30s TTL+加载链 8-worker 并行+前端骨架；热态实测 1.017s→259ms（3.9x），冷态推演用户量级 <1s。
- **tl1485 左栏会话相对时间停旧**（最后消息 11-05 显「1个月」）：数据链定案=边车 UpdatedAt 活动戳语义已死（投递/跨会话追加只写转录不盖戳）。修=SessionListingActivityTime（消息级最新）+只升不降接入 persist+repair 双源；>30 天降级口径核对无恙；裸 max contentMtime 方案被实盘否决（防 #8694 复发）。
- **tl1480 Deny with feedback**：拒绝审批时附反馈文本进 tool_result（原文 strconv.Quote 定界，1000 rune Go/FE 双侧 clamp）；空反馈=纯拒绝现行为字节级不变（三层锁死）；五个拒绝点组装反馈（guardian/write-access/sandbox/config/plan-trust）。
- **tl1427 改写草稿可取消+轨迹 ledger**（cda50ff09，03:2x 补记——初版漏列实为已含）：增强中可取消（双态按钮+迟到结果丢弃竞态钉 33/33）、增强调用进 ModelTrajectory（source=promptEnhance，只记 ledger 不进 transcript）、空草稿提示。

## 门禁与清障

- initial JS 棘轮 645.3→647.2（实测 646.2 超线 0.9，照实测+1.0 一次到位）；largest chunk 棘轮 368.9→370.7（实测 369.7 超线 0.8，照 09-30 步长一次到位）——均为出包清障独立 commit（`7a50fe5d7`/`1409254c1`）。
- 构建三轮清障后 EXIT=0，STAGED 五件+version.txt 20B 无尾。

## 已知边界

- ✅ **推送已完成**：网络恢复后 2 个棘轮 commit 已补推，主线远端=`1409254c1`（含全部 9 支+棘轮）。
- tl1485 已知残留=21 会话偏新 stamp 候另立；tl1427 Go provider TestConnectionReuseBaseline 判预存环境红待核（**本包已含 tl1427**，初版误注已更正）。
