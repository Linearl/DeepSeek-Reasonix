# Release Notes — FORK v1.38.3-20261106-1627（包 80）

> 基线：上一时间戳包 v1.38.3-20261106-0755（包 79）。本包 = 包 79 装机打回三件二轮返修闭环 + 新件四支 + 追加五支，共 17 支。
> 构建点：`820f534dd`。已推 GitHub。

## 打回三件二轮返修闭环（包 79 装机实测打回）

- **切换到此版本二轮**（tl1430b-2）：一轮只修写入链，装机仍不生效——二轮实锤第四环=catalog 投影对 damaged/dirty 日志 fail-closed，把用户刚落盘的显式选择在投影时静默作废（读取链五路倒查：侧栏行/点开对话/重启还原/版本表/链表全依赖投影行角色）。修=严格快照失败回退 RecoveryPreferenceCurrent（只放宽 filing 级显式偏好一环，内容漂移仍拒）。**「人类专属调研员1」装包后可在面板归位（其 damaged 残留正是根因形态）。**
- **归档二轮**（tl1429b-2）：一轮 heal 在拒绝点补了但 re-acquire 源头没断（heal 后 5 秒锁又来）。二轮实锤=并发破坏性流互相 mis-heal（cleanup-pending reconcile/delayed trash/另一 reconcile 无互斥）——四 commit：断源（登记面 in-flight 判定+reconcile 三入口持 sessionRemovalMu 有界等待+delayed trash 先等 teardown）+acquire 侧打点（六调用面 op 标签，acquire/refuse/heal 三线可配对）+.trash 白名单（清空/恢复不再报 outside dirs）+侧栏残留收敛（deleteTopic 两连败后有界 settle 当轮清条目）。
- **改名二轮**（tl1413b）：头部链确实经 RenameTopic 且 bounded 推生效（数据面好），真因=UI 消费端（ProjectTree.refresh 空 options 不重拉普通 topic 页）——修=App 层 topicTitlePatch 单调 seq 乐观补画（与侧栏同构），机制根钉防漂移。

## 新件四支

- **超限态输出不渲染根治**（tl1444，P1）：fence 丢弃分支裸 return 断点——丢弃规则逐字不变+统一调度既有去抖对账→seq-gap 权威重放回填→流式接续不断无需重开 tab；1347 哨兵扩展超限态路径。
- **分支删除侧车清理不全**（tl1442）：侧车全集清单 43 项（guardian 全家/toolstats/pre-repair 群/reminders/imgpack/外部 bak 兜底清扫）+删除计数日志；真实样本构造副本回验 3/3 残留=0。
- **参考页六项交互**（tl1445）：复用 tl1437 同套（选中/右键/双复制/打开/定位/引用），DockSideFileList 共享组件。
- **协作中心两小修**（tl1439）：群聊流贴边对齐单聊+面板记忆持久化（三键，已删优雅回落）。

## 追加五支

- **tl1433 追加四缺口**：左侧胶囊弹层并入 run 行/胶囊计数补工作流子代理(557 同族)/子代理层级缩进+徽标/「最近工作流」历史入口。
- **tl1434 追加**：运行中节点「工作过程」增量可见（写侧 autosave 1.5s 周期快照复用 CAS 协议+登记面即时上报+读侧轮询；只读铁律保持）。
- **tl1435 追加两缺陷**：confirm 节点子代理已结束区漏显补齐+hover 方向改向下。
- **tl1446 双轨八件**（用户批「全做」）：502/504 网关文案/noticeCodeKeys 补映射/约 50 处裸错误 toast 收口 formatActionError/send_failed 人话/fileOpFailed 指引/compaction 本地化/MCP 人话/outcome 复核评估。**双轨红线自查：无隐藏技术细节、无虚构话术。**
- **tl1447 子代理心跳与续轮**：心跳徽章（5min 静默阈值）+「继续」续轮（两步确认）+停止落可续态（failed≠interrupted 语义表收口）。
- **tl1448 调研员1 复合故障行渲染族**：同 stem 双行区分+行↔文件 path 唯一键+删除路径校验三层（渲染层收回/入口复核/发请求前终闸）+主线本体副本行亮「当前选中」徽标收回三入口+版本行身份修（不再冒充「副本」）。

## 文案改进

- 「管理快捷指令」（tl1418，已随包 77）；tl1415b-2/1446/1448/1430b-2 各自三语键随件。

## 门禁与已知边界

- 构建 EXIT=0（十跑；E1×4=门禁收口 rc-detail 样式/bridge tag、JS 棘轮 622.6→636.3、largest gzip 363.2→366.2、locale 双棘轮 zh 125.2/zh-TW 127.3、largest raw 1295→1325——四道预算线全按实测抬到位，独立 commit 报备）。
- **已知边界**：tl1429b-2 后「清理进行中」秒级窗口的横幅文案候装机验证循环消失后再定；tl1447 自动 steer→上报兜底收敛为后续件；tl1434 ephemeral 派发无 transcript 按「无关联」降级（机制非缺陷）；tl1415b-2 版本行第四类型（v3 heads）保留候裁决。
- **环境**：主仓 node_modules 三次被掏实为 zcode 主会话未拆 junction 误删（已更正撤回「外部清理进程」说法）——修复均 pnpm rebuild/install --force。

## 全部提交已推 GitHub（Linearl/DeepSeek-Reasonix main-v2-stable）。
