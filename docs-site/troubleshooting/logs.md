<!--meta title=日志与诊断 desc=找到日志位置、导出诊断信息、使用 doctor 与恢复路径。 -->

# 日志与诊断

## 日志位置

- 桌面端日志在用户数据目录下；设置面板提供「打开日志目录」入口。
- 会话与运行状态在 Reasonix home 下（Windows：`%APPDATA%\reasonix`），目录速查见[配置项参考](../reference/config.html)。
- 需要完整事件回放时，CLI 可用 `--trajectory PATH` 把整次运行记录为 JSONL（工具调用起止时间、思考、重试与恢复决策），便于归因时间去向。

## 导出诊断包

命令面板（`Ctrl+K`）与 CLI 提供诊断导出，反馈问题时附带上下文。

## 诊断命令

```text
reasonix doctor          # 检查配置、桌面派生状态与常见安装问题（不加载 Wails）
reasonix doctor repair   # 在用户明确确认后做安全修复
reasonix crash report    # 崩溃上报（随构建而定；用户授权后才发送）
```

崩溃记录与 pending 更新状态**不会**在下次启动时强制进入全局安全模式；恢复路径由下面的命令与安装布局承担。

## 应用内更新卡住

「设置 → 更新」或顶部横幅提示上次更新未完成（如 `pending update already exists`、`awaiting startup health`）时：

1. 点横幅或设置里的 **「放弃上次更新」**，再点 **「重试」**。
2. 没有该按钮或失败：完全退出后重启一次，再试应用内更新。
3. 仍失败：从官方下载页取最新签名安装包，**直接覆盖安装，不要先卸载**（保留安装根目录，兼容迁移才能证明遗留事务属于哪个安装）。
4. Windows 安装器显示 `Reasonix layout activation failed` 时，展开安装详情复制 `Reasonix layout activator output:` 下方内容随反馈提交。

## 数据恢复

- 会话 JSONL、event log、metadata sidecar 是权威数据；会话/任务 Catalog 只是可重建的查询投影，损坏可删，下次启动重建。
- 合并恢复副本：**设置 → 存储与路径 → 工作阶段版本与副本**，可预览分支、比对计数、勾选合并；被完全覆盖的副本在可恢复回收区。
- 旧版本数据迁移问题见[配置项参考](../reference/config.html#旧版本迁移)与 `/migrate` 命令。

## 下一步

- [常见问题 FAQ](faq.html)
- [配置项参考](../reference/config.html)
