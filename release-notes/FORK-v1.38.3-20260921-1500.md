# FORK v1.38.3-20260921-1500

> 基线：`FORK-v1.38.3.md` ｜ 本版仅列相对**上一时间戳包 0740** 的新增
> 类型：**新特性 × 2 + 观测 + 告警口径修正**

## 新特性

**provider `hidden` 字段：把「被建成 provider 的模型」从界面收起来（零迁移）。**

- 同一后端被配置成多个 provider（如 DeepSeek 3 个、MiMo 4 个）时，界面各处的供应商列表会被重复项淹没。现在 `config.toml` 的 provider 支持 `hidden = true`：**所有界面（设置面板连接列表 / 模型分配下拉 / 用量页分组 / 模型选择器 / 任务配置「模型覆盖」/ CLI picker）不再显示它**，但 `<provider>/<model>` 引用**照常解析** —— 既有会话、任务、标签页状态**零迁移、零风险**。
- 不写 `hidden` 字段的 provider 行为与旧版**逐字一致**；保存路径对未知字段是「不修改」语义，旧前端不会意外取消隐藏。
- 取消隐藏目前需改 config（UI 入口暂不做，等真实需要）。

**跨会话消息 hop 上限开放到实验区（3~1000，默认 5）。**

- 跨会话消息链的轮次上限此前硬编码为 5，多轮协作容易触顶被迫开新线程。现在设置 → 实验特性新增「hop 上限」数值项：**范围 3~1000，越界自动钳制**，拒绝文案动态报出当前上限（如 `collaboration chain hop limit reached (max 8)`）。
- 实现：上限由邮件存储**实例持有**（无包级可变全局）、配置**调用时求值**（改完即生效，无需重启）；**发送与接收两个方向都执行同一上限**（调低后，在途的超限消息会被拒绝而不是投递出去）。
- **默认 5 的行为与旧版逐字一致**（含错误文案，测试固定）。

## 诊断观测（196 第三轮：save 侧全量重放的「为什么」）

上一包回答了「谁在等锁」（`Session.save` 自己在锁内全量重放 DAG，20–30 秒）。本包回答「**为什么走了全量**」：

| 观测 | 用途 |
|---|---|
| `dag state for save extended=<bool> reason=<枚举>` | 复用条件失配时**具名理由**：`nil_cache` / `no_header` / `path_mismatch` / `generation` / `damaged` / `tail_truncated` / `size_regression` —— 每种理由对应不同的修法形态，读数一次拿全，避免第二轮盲修 |
| perf 监控 `eventsMb` 排除 `.trash` | 告警口径修正：删除的会话移入回收区后不再计入（此前两个已删除大会话的 1.2GB 死文件把告警推到 2GB+，淹没真实水位） |

## 装机验证清单

- [ ] 在 `config.toml` 给 `deepseek-flash` / `deepseek-pro` / `mimo-pro` / `mimo-flash` 四个连接加 `hidden = true` 后重启：设置面板 / 模型选择器 / 「模型覆盖」下拉 / CLI picker 只剩 1 个 DeepSeek 与 2 个 MiMo；既有会话与任务的模型引用仍正常工作
- [ ] 实验区把 hop 上限设为 8：hop≤8 的跨会话回复正常送达；把上限调回 5：行为与旧版完全一致
- [ ] 打开回收站（含大文件会话）：`logs/desktop/desktop.log` 中 `eventsMb` 告警不再计入回收区体积
- [ ] 切换「测试会话」等大会话后取诊断读数：

```bash
grep -E "dag state for save" desktop.log    # extended + reason 失配因分布（196 修法的点火依据）
grep -E "perf monitor threshold" desktop.log # eventsMb 真实水位（不再含 .trash）
```

## 未含项

- 任务 196 的**修法本体**：按设计须等本包 `reason` 读数确认失配因分布后再开工（七种理由对应不同触碰点，盲写有违反写路径契约的风险）。
- 任务 173 面板其余 8 个子项（require_reply / 工具门控等）未实施；本包只落了 hop 数值项骨架，173 可直接沿用。
- `-race` 竞态验证未做（本机缺 C 编译器）。
