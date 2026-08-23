# Reasonix Fork: main-v2-stable

> 临时补丁分支：解决并行工作核心痛点，不长期分叉。
> 基线：官方 `v1.31.3`（commit `b9cf32f81`，2026-08-22）。
> 目标：上游合并 #9111 / #9214 后废弃本分支，回归官方主线。

## 分支内容

相对 `v1.31.3` 只包含以下提交：

| Commit | 内容 | 来源 |
|---|---|---|
| `86faecef0` | docs(prompt): prefer file-grained write_paths over directories | #9111 |
| `c9fb58bc9` | feat(agent): optimistic-concurrency writes (write-if-unchanged) | #9214 |
| `71be5b6b3` | feat(desktop): add parallel-write safety check toggle | #9214 |

说明：原 `52640f22a`（path-grained write coordination）未纳入，因为上游 `v1.31.3`
已包含等价实现（`514e39f2e`、`9bd3518ca` 等），避免重复和冲突。

## 数据兼容红线

- 不修改 `*.jsonl`、`*.events.jsonl`（WAL）、`*.meta`、`*.context.json` schema。
- 不修改 checkpoint v3 格式。
- 不修改 `config_version`。
- 不修改 CLI / ACP / 扩展协议 / Provider 请求序列化。
- 不改默认数据路径：`~/.reasonix` / `%APPDATA%\reasonix`。
- 不修改 `desktop/updater*.go`、`desktop/internal/update/`、`internal/repair/update.go`。

## Release 与安装

- fork 仓库自行出 release 包，走正常安装流程覆盖官方安装。
- 版本号建议 `v1.31.3+fork` 或 `v1.31.3`（semver 兼容，避免 `-fork` pre-release）。
- 关闭自动更新：`[desktop] check_updates = false`。
- 不要删除/替换 `reasonix-update-helper.exe`。
- 回迁官方：优先官方安装器覆盖安装；也可重新开启 `check_updates` 等官方更高版本自动更新。

### Fork release 产物

- CLI：`bash scripts/fork-release.sh [version]` 本地构建多平台 CLI（unsigned）。
- Desktop：`.github/workflows/release-fork.yml` 手动触发，构建 unsigned 安装包/归档并发布到 fork 的 GitHub Release。
- 注意：fork release 不做 MINISIGN/SignPath/Apple 签名，不生成 `latest.json`，不镜像 R2；因此 fork 版请走手动安装，不要依赖自动更新。

## 验证状态

- [x] `go build ./cmd/reasonix` 通过。
- [x] 添加 `scripts/fork-release.sh`（CLI release 构建脚本）。
- [x] 添加 `.github/workflows/release-fork.yml`（最小 unsigned desktop release workflow）。
- [ ] `go test ./...`、`make vet`、`scripts/cache-guard.sh`（用户正在执行，等待结果）。
- [ ] 安装 fork release 后验证并行写效果、自动化任务、周报正常。
