<!--meta title=常见问题 FAQ desc=安装、配置、数据与故障的常见问题速答。 -->

# 常见问题 FAQ

## 安装与版本

**Q：本 fork 和官方版是什么关系？**
A：本仓库是 [esengine/DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix) 的长期维护 fork。会话数据与上游完全兼容（按上游格式写、宽松读，另加修复路径）；随时可用官方安装包覆盖切回。

**Q：哪些版本是稳定的？**
A：`1.31.4` – `1.34` 与 `1.38.3`。`1.38.1` 已知不稳定，请勿使用。本 fork 只验证 Windows 桌面版 + 经典布局。

**Q：桌面端和 CLI 要分别配置吗？**
A：不用。两端共用同一份全局配置（Reasonix home 下的 `config.toml` 与 `.env`），一端配置另一端直接可用。

## 配置与凭据

**Q：API Key 存在哪里？会泄露给模型或工具吗？**
A：密钥值只存 `<Reasonix home>/.env`；`config.toml` 里只有 `api_key_env` 变量名。模型、文件读取工具、受沙箱保护的 shell 命令与 MCP server 都读不到全局凭据。

**Q：配置改了不生效？**
A：检查配置优先级：命令行参数 > 项目 `./reasonix.toml` > 全局 `config.toml` > 旧配置 > 内置默认。项目级覆盖会压过全局值。

**Q：从很旧的版本升级，数据会丢吗？**
A：v1.8.1 起启动时自动做一次性非破坏性迁移（旧文件保留）。没迁到可以在会话里跑 `/migrate`；旧 v0.x 自定义数据目录用 `/migrate --from "<旧目录>"`（只导入 sessions）。

## 日常使用

**Q：命令面板怎么打开？**
A：Windows/Linux `Ctrl+K`（macOS `Cmd+K`）。产品文档入口也在面板里，或直接按 `F1`（fork 新增）。全部快捷键见[快捷键](../getting-started/shortcuts.html)。

**Q：怎么让任务全自动执行？**
A：一次性任务用 `reasonix run --auto "..."`；交互会话用 `Ctrl+Y` 切 YOLO 模式。只建议在可信任务与受控目录使用；配合逐轮 checkpoint 可随时 `/rewind` 回退。

**Q：上下文太长被压缩，关键决策丢了？**
A：把关键决策写进项目文档（如 AGENTS.md / 记忆）；压缩前会先 snip/prune 旧工具输出再做摘要。可用 `/context` 查看占用、`/compact` 手动压缩、`reasonix config compact-ratio` 调阈值。

**Q：多个会话同时写一个项目会互相卡住吗？**
A：默认整工作区串行写锁（安全）。fork 提供乐观并发写入（write-if-unchanged 陈旧检测），可在设置 → 沙箱/权限中改并行 + 逐写校验。

## 故障

**Q：应用更新卡住 / 提示 pending update？**
A：见[日志与诊断](logs.html#应用内更新卡住)：先「放弃上次更新 → 重试」，不行就官方安装包覆盖安装，**不要先卸载**。

**Q：怀疑配置或安装坏了？**
A：跑 `reasonix doctor` 检查；确认后 `reasonix doctor repair` 做安全修复。

**Q：会话列表空了 / Catalog 损坏？**
A：会话 JSONL 与 event log 是权威数据，Catalog 只是可重建投影，删除后下次启动自动重建。

## 下一步

- [日志与诊断](logs.html)
- [回到首页](../)
