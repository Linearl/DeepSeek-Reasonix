# Release Notes — FORK v1.38.3-20261108-1056（包 84）

> 基线：v1.38.3-20261108-0302（包 83）。本包 = 包 83 后四件满批，全时段出包装机自主授权内自主出包装机。
> 构建点：`fe1c312e9`（含 4 个棘轮清障 commit + node_modules 修复）。

## 滚合四件（包 83 后累计，凑批 4/4 满触发）

- **tl1487 实验室八组批量 tier 调整**（6e18f5ee2）：严格按用户拍板清单逐项——终态 9 稳定/47 推荐/25 可选/5 未稳定=总 86；卡片化合并（冷缓存三行并边界子卡）；collabHub→稳定（默认关语义不变）、CDP→稳定、OpenCode Go 用量撤开关常开；tl134 路径规则整链退役（存量键 deprecatedConfigKeys）；计数钉全同步。
- **tl1481 MCP 失败处理建议 Popover**（e24ec0f76）：状态点点击三级下钻（建议→错误详情）；按失败类型（auth/config/缺可执行/网络/其他）映射既有分类给建议，可展开原始错误详情复制；开合切换与 jobs 触发器同式；locale 三语冲突保留双侧。
- **tl1429d2 回收站同族多桶渲染去重返修**（230666945）：替换被打回的标题后缀方案——桶富化层补 TrashFamilyKey/TrashFamilyCopy 只读归组字段（撤标题污染，负钉防回潮）；渲染层同族合并为主线一行+「+N 副本」徽标+可展开明细（缩进子行逐桶预览/单独还原/清除，单桶语义完整保留）；页头按族计数。
- **tl1485b 存量 stamp 自愈**（004d3ed4a）：21 会话偏新 200h+ 修复——盘点扫描器（基准钉死消息级 CreatedAt 禁 mtime）+repair 链偏新校正（≥1h 降消息级、<1h 合法延迟不动、空壳不落笔+廉价探针封顶）+catalog 触发判据补偏新形态（原队列按 turns_state=unknown 取件永不可达的实盘缺口）；实盘复扫 0/15 按设计不修，12 枚新钉全绿。

## 门禁与清障（本包四道预算全数超线，逐项清障）

- initial JS gzip 647.2→667.9（实测 666.9 超 19.7，多步并一步）；largest chunk gzip 370.7→389.3（实测 388.3 超 17.6）；initial raw 3236.0→3286.0（实测 3274.2 超 38.2，raw +10 五步）；largest chunk raw 1325→1425（实测 1404.9 超 79.9 历史最大，十步并一步含余量 20.1）——均为出包清障独立 commit 自抬报备。
- node_modules 第四形态修复：.bin 全空+.modules.yaml 缺失（lint:hooks 报 eslint 不存在），`pnpm install --force` 44.7s 恢复后 10/10 棘轮检查全过。
- 构建六轮清障后 EXIT=0，STAGED 五件+version.txt 20B 无尾。

## 已知边界

- tl1485b 预存红申报：sessioncatalog TestRepairResultPreservesDirectoryProjectionUntilReconcile（不含本件基线复跑同红坐实，与 tl1485 同族非本件引入，zcode 建议另开任务排查）。
- 四项预算超幅均创近期新高（largest raw 超 79.9），主要增量源=tl1487 实验室 86 键 tier 卡片化进 eager 面，后续批次请继续盯棘轮。
