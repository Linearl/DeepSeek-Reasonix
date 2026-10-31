# Reasonix Fork 桌面版 v1.38.3-20261031-1534 — Release Notes

> 基于 v1.38.3-20261031-0912（包60）以来：单一主题修复——大会话「turn 账本写入被文件占用拒绝 → 自动续轮熔断」问题。受影响的会话升级后**无需 reopen，等 30 秒冷却自动恢复**（或回复「继续」触发）。

## 本版内容

- **修复：大会话「不再可写 / 自动续轮熔断」（tl1292）**：当会话事件日志很大（如 fork开发-新10 的 86MB）时，每轮开轮要对日志做一次「临时文件 + 原子替换」写入；Windows 上任何并发读者（守护/清扫/上下文构建/杀软扫描）在读取期间持有句柄，替换就会被拒（Access is denied）。旧版有三个叠加缺陷：①替换重试总窗口仅 1.56 秒，撑不过大文件扫描时长；②首次失败后 turn 账本被永久标记不可用，后续每轮直接拒绝同一错误、不再真正重试；③只能手动关闭再重开会话才能解锁。本版：共享锁类失败的退避窗口拉长 10 倍（→15.6 秒）覆盖大日志扫描时长；账本标记改为可自愈（30 秒冷却后自动清标记重开写入通道重试）；替换重试基数对「共享锁」类失败专项放宽。三层叠加后，一次占用最多延迟一轮，不再锁死。
- **顺带清障（tl1238/tl1282 滚合带入的门禁违例）**：信件批量栏/全选栏两处裸 `z-index` 换 `--z-app-content` token；检索历史下拉阴影的硬编码颜色换 `color-mix(var(--shadow-color))`（视觉逐字节等价）。

## 使用提示

- 升级后：之前卡在「input was not accepted: this session is no longer writable」的会话（如 fork开发-新10），**不需要关闭重开**——等 30 秒冷却后回复「继续」即可正常续轮；即便再遇到占用，也只会延迟一轮而不会锁死。
- 事件日志较大的会话保存仍会有一次大写入（每轮 86MB 级替换），属既有行为；日志瘦身能力（tl1285）后续批次继续优化。

## 门禁与质量

- 定向：fileutil + turnevent 包全绿；control admission 定向 6/6；negative-path 由新增 build-tag 判定（replacelock_windows/other）钉住 Windows sharing-lock 分类。
- 出包插曲：两道前端门禁红（check:z-index ×2、check:class-contract ×1）均为 09:43 补滚合（tl1238/tl1282）带入的违例，已按 token 语义等价修复（z-index → --z-app-content；rgba → color-mix(--shadow-color)），第三次构建 EXIT=0。
- wails 官方管线 + ldflags 版本注入 1.38.3-20261031-1534，staging 四件套已铺。
