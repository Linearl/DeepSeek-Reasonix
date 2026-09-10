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

## 验证状态（2026-09-10 更新）

- [x] `go build ./...` 主 module 与 `cd desktop && go build ./...` 均通过。
- [x] `scripts/fork-release.sh`（CLI release 构建脚本）。
- [x] `.github/workflows/release-fork.yml`（unsigned desktop release workflow，手动触发）。
- [x] 本地安装包：`scripts/build-local-installer.sh`（rsrc 图标注入 + wails build + NSIS）。
- [x] 测试：`internal/agent` 111s / `internal/control` 76s / `internal/recovery` / `evidence` / `tool/builtin` / `goaleval` / `boot` 全绿；desktop 定向（ServePool / Heartbeat / Settings）52s 通过。
- [x] `tsc --noEmit` 0 错误；`node scripts/check-fork-integrity.mjs` **30/30**。
- [x] v1.38.3 已发布（tag `desktop-v1.38.3` + 9 平台产物，run 34340502890 success）。

## 发布前检查清单（fork desktop）

1. **代码**：`git status` 干净 → `go build ./...` → `cd desktop && go build ./...` → `cd desktop/frontend && npx tsc --noEmit` → `node scripts/check-fork-integrity.mjs`（须 30/30）
2. **文档**：`release-notes/FORK-vX.Y.Z.md` 含本版全部改动；`desktop/wails.json` 的 `productVersion` 与 tag 版本一致
3. **本地包**（推荐先跑一遍）：`nohup bash scripts/build-local-installer.sh > /tmp/build.log 2>&1 & disown`（**加** `preserve_background_processes`；前台 115s 会被 SIGTERM，MSYS 无 `setsid`）→ 装后走关键路径
4. **推送**：`git push origin main-v2-stable`
5. **tag**：先 `gh release delete desktop-vX.Y.Z -R Linearl/DeepSeek-Reasonix`（**不带** `--cleanup-tag`）→ 删远端 tag → 再推 tag。顺序反了会把重推的同名 tag 一并删掉
6. **dispatch**：`gh workflow run release-fork.yml -R Linearl/DeepSeek-Reasonix -f tag=desktop-vX.Y.Z`
7. **验证**：`gh release view desktop-vX.Y.Z -R Linearl/DeepSeek-Reasonix`，确认 9 平台产物齐全

**两个必背的坑**：① `gh` 默认解析到 upstream（esengine）→ 必须显式 `-R Linearl/DeepSeek-Reasonix`，否则 404；② fine-grained PAT 会 403 → 用 `env -u GITHUB_TOKEN gh ...` 切 keyring OAuth token。
