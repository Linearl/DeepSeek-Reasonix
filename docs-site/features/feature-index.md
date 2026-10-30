<!--meta title="全部特性一览表" desc="fork 全部特性的编号、档位与所在分组；编号规则与徽章四档说明。" -->

# 全部特性一览表

本表是 fork 全部特性的编号索引：**81 项**实验特性（骨架来自实验室设置页的真实组织方式）+ **6 项**已释放增强（9xx 段，见一览末节）。编号一经分配不再变更，特性退役后编号保留不复用。

## 编号规则

3 位编号，**百位=分组**，段内按实验室设置页顺序递增：

| 段 | 分组 | 范围 | 说明 |
| --- | --- | --- | --- |
| 1xx | 自动化 | 101-110 | 心跳、跨会话协作、智能自动化等 |
| 2xx | 记忆与上下文 | 201 | 置顶记忆索引化 |
| 3xx | 提效 | 301-315 | 压缩、预算、消息合并、快捷指令等 |
| 4xx | 界面 | 401-430 | 视图、标签页、检索、子代理面板等 |
| 5xx | 可观测性 | 501-505 | 监控、用量监测、日志管理 |
| 6xx | 开发调试 | 601-602 | CDP 端口、噪音分诊 |
| 7xx | 存储 | 701-706 | 存储版本、归档、闲置释放、内存上限 |
| 8xx | 基础设施 | 801-812 | 运行时复用、工具优化、路径规则等 |
| 9xx | 已释放增强 | 901- | 已移出实验室面板、成为正式能力的 fork 增强 |

## 徽章四档

初标依据 = 实验室设置页当前的档位分类；档位会随打磨进度调整，以本表与应用内实验室面板为准。

| 徽章 | 含义 | 本表数量 |
| --- | --- | --- |
| <span class="badge badge-stable">已转正</span> | 已释放到实验室面板之外的正式能力 | 4（+9xx 段） |
| <span class="badge badge-recommended">推荐</span> | 已打磨可用，建议开启 | 20 |
| <span class="badge badge-optional">可选</span> | 按需开启，默认关闭 | 45 |
| <span class="badge badge-unstable">未稳定</span> | 仍在打磨，行为可能变化 | 12 |

## 一览

### 自动化

| 编号 | 特性 | 徽章 | 实验室位置 |
| --- | --- | --- | --- |
| <span class="fnum">101</span> | 自动驾驶 | <span class="badge badge-stable">已转正</span> | 实验特性 |
| <span class="fnum">102</span> | 自动折叠跨会话消息 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">103</span> | 跨会话协作 | <span class="badge badge-stable">已转正</span> | 实验特性 |
| <span class="fnum">104</span> | 完全访问（yolo） | <span class="badge badge-stable">已转正</span> | 实验特性 |
| <span class="fnum">105</span> | 梦境整理 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">106</span> | 决策模式提取 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">107</span> | 心跳任务后台化 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">108</span> | 模型提前结束续轮提醒 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">109</span> | 普通对话续跑（实验） | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">110</span> | 智能自动化 | <span class="badge badge-optional">可选</span> | 实验特性 |

### 记忆与上下文

| 编号 | 特性 | 徽章 | 实验室位置 |
| --- | --- | --- | --- |
| <span class="fnum">201</span> | 置顶记忆索引化 | <span class="badge badge-unstable">未稳定</span> | 实验特性 |

### 提效

| 编号 | 特性 | 徽章 | 实验室位置 |
| --- | --- | --- | --- |
| <span class="fnum">301</span> | 乐观并行 | <span class="badge badge-recommended">推荐</span> | 实验特性 |
| <span class="fnum">302</span> | 分片压缩并行化 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">303</span> | 每轮上下文预算 | <span class="badge badge-recommended">推荐</span> | 实验特性 · 子项 |
| <span class="fnum">304</span> | 主动压缩冷却（可配） | <span class="badge badge-recommended">推荐</span> | 实验特性 · 子项 |
| <span class="fnum">305</span> | 缓存调优 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">306</span> | 允许指定压缩模型 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">307</span> | 消息合并 | <span class="badge badge-recommended">推荐</span> | 实验特性 · 子项 |
| <span class="fnum">308</span> | 心跳会话轮换 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">309</span> | 安全 / 成本控制 | <span class="badge badge-optional">可选</span> | 实验特性 |
| <span class="fnum">310</span> | 高速模型 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">311</span> | 快捷指令 | <span class="badge badge-recommended">推荐</span> | 实验特性 · 子项 |
| <span class="fnum">312</span> | 轨迹状态 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">313</span> | 回答风格 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">314</span> | 交互与呈现 | <span class="badge badge-recommended">推荐</span> | 实验特性 |
| <span class="fnum">315</span> | 协作中心 | <span class="badge badge-unstable">未稳定</span> | 实验特性 |

### 界面

| 编号 | 特性 | 徽章 | 实验室位置 |
| --- | --- | --- | --- |
| <span class="fnum">401</span> | 启动遮罩 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">402</span> | 启动与加载 | <span class="badge badge-optional">可选</span> | 实验特性 |
| <span class="fnum">403</span> | 标签页管理 | <span class="badge badge-recommended">推荐</span> | 实验特性 |
| <span class="fnum">404</span> | 分栏 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">405</span> | 轨迹视图 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">406</span> | 左边栏悬停展开 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">407</span> | 右边栏悬停展开 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">408</span> | 对话权限下拉框 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">409</span> | 表格按栏宽换行 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">410</span> | 发送目标下拉框 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">411</span> | 流式滚动平滑跟随 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">412</span> | 草稿优化 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">413</span> | 视图布局 | <span class="badge badge-recommended">推荐</span> | 实验特性 |
| <span class="fnum">414</span> | 侧栏增强 | <span class="badge badge-recommended">推荐</span> | 实验特性 |
| <span class="fnum">415</span> | 历史选择器 | <span class="badge badge-recommended">推荐</span> | 实验特性 · 子项 |
| <span class="fnum">416</span> | 搜索历史提问 | <span class="badge badge-recommended">推荐</span> | 实验特性 · 子项 |
| <span class="fnum">417</span> | 自动加载 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">418</span> | 检索与历史 | <span class="badge badge-recommended">推荐</span> | 实验特性 |
| <span class="fnum">419</span> | 会话图墙 | <span class="badge badge-recommended">推荐</span> | 实验特性 |
| <span class="fnum">420</span> | 工作流面板 | <span class="badge badge-unstable">未稳定</span> | 实验特性 |
| <span class="fnum">421</span> | 子代理面板 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">422</span> | 子代理详情视图 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">423</span> | 子代理委派档位 | <span class="badge badge-unstable">未稳定</span> | 实验特性 · 子项 |
| <span class="fnum">424</span> | 子代理进度吞吐 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">425</span> | 任务完成摘要 | <span class="badge badge-optional">可选</span> | 实验特性 |
| <span class="fnum">426</span> | 输入框草稿持久化 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">427</span> | 选区快捷操作 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">428</span> | 输入与选区 | <span class="badge badge-optional">可选</span> | 实验特性 |
| <span class="fnum">429</span> | 意见箱 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">430</span> | 重启更新 | <span class="badge badge-recommended">推荐</span> | 实验特性 · 子项 |

### 可观测性

| 编号 | 特性 | 徽章 | 实验室位置 |
| --- | --- | --- | --- |
| <span class="fnum">501</span> | 监控（会话 + 性能） | <span class="badge badge-recommended">推荐</span> | 实验特性 |
| <span class="fnum">502</span> | GLM Plan 用量 | <span class="badge badge-recommended">推荐</span> | 实验特性 · 子项 |
| <span class="fnum">503</span> | OpenCode Go 用量 | <span class="badge badge-unstable">未稳定</span> | 实验特性 · 子项 |
| <span class="fnum">504</span> | 用量监测 | <span class="badge badge-recommended">推荐</span> | 实验特性 |
| <span class="fnum">505</span> | 日志管理 | <span class="badge badge-optional">可选</span> | 实验特性 |

### 开发调试

| 编号 | 特性 | 徽章 | 实验室位置 |
| --- | --- | --- | --- |
| <span class="fnum">601</span> | CDP 调试端口 | <span class="badge badge-unstable">未稳定</span> | 实验特性 · 子项 |
| <span class="fnum">602</span> | 崩溃 lifecycle 噪音分诊 | <span class="badge badge-unstable">未稳定</span> | 实验特性 · 子项 |

### 存储

| 编号 | 特性 | 徽章 | 实验室位置 |
| --- | --- | --- | --- |
| <span class="fnum">701</span> | 存储版本 | <span class="badge badge-unstable">未稳定</span> | 实验特性 · 子项 |
| <span class="fnum">702</span> | 修复会话 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">703</span> | 数据目录 | <span class="badge badge-unstable">未稳定</span> | 实验特性 |
| <span class="fnum">704</span> | 冷会话归档（zstd） | <span class="badge badge-optional">可选</span> | 实验特性 |
| <span class="fnum">705</span> | 后台会话闲置释放 | <span class="badge badge-recommended">推荐</span> | 实验特性 |
| <span class="fnum">706</span> | Go 软内存上限 | <span class="badge badge-optional">可选</span> | 实验特性 |

### 基础设施

| 编号 | 特性 | 徽章 | 实验室位置 |
| --- | --- | --- | --- |
| <span class="fnum">801</span> | 运行时复用池（task 363A） | <span class="badge badge-stable">已转正</span> | 实验特性 |
| <span class="fnum">802</span> | ACP 常驻监听 | <span class="badge badge-unstable">未稳定</span> | 实验特性 |
| <span class="fnum">803</span> | 路径规则 | <span class="badge badge-unstable">未稳定</span> | 实验特性 |
| <span class="fnum">804</span> | 孤儿资源回收清理 | <span class="badge badge-recommended">推荐</span> | 实验特性 |
| <span class="fnum">805</span> | 本地服务 | <span class="badge badge-unstable">未稳定</span> | 实验特性 |
| <span class="fnum">806</span> | 编辑证据门宽松模式 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">807</span> | multi_edit 直注 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">808</span> | 能力组披露与 session_hub 聚合 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">809</span> | multi_edit 错误详情增强 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">810</span> | multi_edit 证据门连坐收窄 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">811</span> | multi_edit 跳过失败项 | <span class="badge badge-optional">可选</span> | 实验特性 · 子项 |
| <span class="fnum">812</span> | 工具优化（工具表开关） | <span class="badge badge-recommended">推荐</span> | 实验特性 |

### 9xx · 已释放增强（正式能力）

以下 fork 增强已移出实验室面板、成为正式能力——**无需开开关，装完即有**。编号保留在此供追溯。

| 编号 | 能力 | 入口 |
| --- | --- | --- |
| <span class="fnum">901</span> | 项目分组（折叠状态跨重启保持） | 项目树头部「新建分组」；项目右键「移动到分组」 |
| <span class="fnum">902</span> | 项目颜色筛选与排序（多选） | 项目树头部调色板按钮 |
| <span class="fnum">903</span> | 合并恢复副本管理（主线/总计/共有/独有/孤立计数，可预览、比对、勾选合并） | 设置 → 存储与路径 → 工作阶段版本与副本 |
| <span class="fnum">904</span> | 计划任务模型覆盖（每条心跳任务单独指定 provider / 模型） | 侧边栏「自动化」→ 任务编辑 |
| <span class="fnum">905</span> | 已授权写目录面板（会话级授权可视化 + 全局公共写目录，跨项目永久生效） | 设置 → 权限 → 已授权写目录 |
| <span class="fnum">906</span> | 只读研究预算加倍（`extend_research_budget`，轮次预算 10→20→40→80） | 只读调研触发软预算收敛后自动可用 |

