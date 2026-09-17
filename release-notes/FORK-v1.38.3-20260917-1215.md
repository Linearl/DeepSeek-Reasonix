# Reasonix Fork 桌面版 v1.38.3-20260917-1215 — Release Notes

> **构建**：2026-09-17 12:1x ｜ **安装目录**：`versions/v1.38.3-20260917-1215/`
> **基线**：官方 `v1.38.3`。本仓库长期停在该版本，包版本带时间戳只为区分**同一版本号下的多次构建**。
>
> 本文件**只列本版相对上一版包的新增内容**。基线版本的完整说明见 `FORK-v1.38.3.md`；
> 逐项差异台账见 `FORK-vs-upstream.md`。

## 本版新增：任务 154 创建即注册 + 独立审计三轮复核通过

**141 + 154 一锅端**（C 路线）。经独立审计三轮复核（报告 `issues/reports/任务154-创建即注册与删除会话-代码审计报告-20260917.md`），结论「可以出包」。

- **创建即注册**：`create_collab_session` 创建后立即写 transcript + contact_id + purpose，**无需先打开会话**即可被 `list_addressable_sessions` / `talk_to_session` 找到
- **141 三项隐藏验收**：contact_id 随机化（不再从文件名派生）、侧车过滤 `IsMainTranscript` 统一（委托 `store`）、purpose 创建即写
- **`delete_session` 工具**：dry-run 无副作用返回影响面（openTab/HasTurn 从 live tab map 填）；confirm 移回收区（manual restore）；拒绝删除调用方自身
- **审计修掉的 5 个必修缺陷**：dry-run 误删、影响面恒 false、`.guardian.jsonl` 漏进通讯录、`30d` 假承诺、`finishDestroyHandles` 缺失

已知残留见审计报告六节（fallback 未透出、desktop 侧单测未补等，均判定不阻塞）。

## 本版新增：通讯录搜索 + 归档语义澄清 + v4 双写去重

**搜索接口**：新增 `search_sessions(query)`，按标题/职责关键字（也匹配 contact_id / topic_id）
检索通讯录。会话多、200 条分页看不到时用它，不必翻页。默认 `limit=50`、上限 500。

**归档 vs 删除（实测澄清）**：
- **删除**的会话在 `.trash/`，`ScanDir` 不递归进去——**从不进入通讯录**。
- `archive/` 是**长期归档的旧会话**（你的机器上 366 条，2026-06-12 ~ 08-17），不是删除。
  默认列表**不包含**归档；要查用 `archived: true`。`total` 现在与过滤口径一致，
  不再把隐藏的归档算进总数。

**v4 双写去重**：开启 `session_storage=v4` 后同一对话会在 `sessions/` 与 `sessions-v4/`
各存一份。通讯录**只扫 legacy `sessions/` 树**（它是权威副本），再加一层按文件名 stem 去重，
保证同一对话只计一次。

## 本版新增：通讯录瘦身（只回元信息）

`list_addressable_sessions` 改为**只返回对话元信息**（标题 / 职责 / contact_id / topic_id），
不再夹带路径，也从不包含转录内容。内容请用独立接口 `read_session_tail(target, max_bytes)`
（默认 10 KiB，`max_bytes` 可调，上限 64 KiB）。

列表**按最近更新排序 + 分页**：`limit` 默认 200、上限 1000。上一版一次列表可撑到 70KB 被截断，
本版改为紧凑 JSON 元数据。

**旧会话可用**：工具在会话控制器构建时注册，进程重启后所有会话（含此前建的）在下一次发消息时
都会带上这套工具——不限新建会话。

## 上一版修复（保留）：重启后仍无通讯录 + 系统提示点名

开关写了 `[desktop]` 与 `[agent]` 两处，但 boot/技能门控只读 `[agent]`；只读到 Desktop 镜像时
工具整组不注册。本版两处 **OR**（与 Trace-as-State 同）。系统提示用中文点名「通讯录」及其工具。

## 上一版新增（保留）：通讯录全量会话 + 后台可收信 + 职责登记四项

- 通讯录 = 全部会话（职责可选，标题可寻址，首触自动铸 contact_id，重名报错）
- 投递不要求标签页开着：detached 运行时直投，完全关闭的会话自动打开
- 职责登记：可替其他会话登记、`read_session_tail` 读末尾再定职责、先问再登记、创建会话必须登记用途、可更改自己的用途
