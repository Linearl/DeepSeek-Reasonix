# Fork v1.38.3-20261001-1210（七批包：1107 + 352 + 434 两件增量）

> 基线：v1.38.3-20261001-1107。增量两件，integrity 191/191。

## 增量

- **352 侧栏长数字文件名行**：根因修复=create/recover 窗口期 runtime 路径不再发 session-kind 子节点（一 topic 一逻辑行+多 snapshot 聚合，open 赢 representative/running 赢 status）——消灭「长数字名+[之前]」的分型来源而非修显示；守卫测试两枚钉死契约
- **434 serve 启动 panic 根治（P0）**：bus_mcp `/mcp` 注册改方法约束对齐（POST|GET|DELETE）终结与 `GET /` 的 ServeMux 歧义冲突——bus_mcp enabled 时 serve 可正常启动；回归钉三断言（三方法达 bus 鉴权 401+GET / 与未知路径仍 200）

## 验证状态

- 434 修复后本机将重启用 bus_mcp 完成 431 联调（enroll 已验证配对）
- 352 待装机目验侧栏
