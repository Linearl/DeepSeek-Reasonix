# Reasonix Fork 发版指南（RELEASING.zh-CN）

> 2026-11-19 发版实战沉淀（desktop-v1.38.3-20261119-2016 全链走通 + 两轮构建踩坑）。
> 配套：`release-notes/WRITING-GUIDE.md`（notes 写作规范）· 技能 `ll-fork-guide` §5（速查层）。
> 适用：正式 GitHub Release 发版（含本地出包装机）。过程包（内部验证）只做「构建+台账+changelog」，跳过 tag/CI/公告。

---

## 一、子任务清单（按序执行）

### 阶段 0 · 盘点
1. **确认基线**：`gh release list -R Linearl/DeepSeek-Reasonix --limit 3` 取**上一个发布的版本**（发版级 notes 的增量基线，不是上一时间戳包！）。
2. **盘点本版内容**：tl 未完成/本段 done 清单 + `git log <基线tag>..HEAD --oneline`，确定主线大特性（2~4 条）与「其他改进」条目。
3. **出包快照纪律**：要带的代码必须**先合入主线再启动构建**（build 启动后的 commit 不进本包，见 ll-fork-guide §8）。

### 阶段 1 · 材料四件套（构建前完成）
4. **发版级 notes**：`release-notes/FORK-v<版本>.md`（文件名=包版本，精确到分钟）。七段结构：头部引用块（必含 `FORK-vs-upstream.md` 链接）/概览/主线特性（用户视角：效果→入口→原理 1~2 句）/其他改进/升级提醒（5 必含项）/安装。写法细则见 WRITING-GUIDE §4-5。
5. **差异台账**：`release-notes/FORK-vs-upstream.md` **顶部追加本版表**（改动|来源|上游吸收状态）。⚠️ `release-fork.yml` 会 grep 本文件，**缺本版表=workflow 直接失败**。上一版漏记的在此补注并入。
6. **文档站 changelog**：`docs-site/changelog/<YYYY-MM>.md` 顶部插当日日期节，每个包一条（`### v<版本> <span class="vtag vtag-release">正式 Release</span>`），**更新头部统计行**（本页 N 个包…）。push 自动触发 `Deploy docs site`。
7. **README**：告示类内容按需增删（原则：问题修复了就把告示**直接删掉**，不改「已修复」版长期挂着——用户 20261119 定）。

### 阶段 2 · 构建与装机
8. **构建**（agent 内必须注入 PATH，两个都踩过）：
   ```bash
   export PATH="$HOME/go/bin:/c/Program Files/Go/bin:/c/Program Files/nodejs:$HOME/AppData/Local/reasonix-nsis/nsis-3.12/Bin:$PATH"
   bash scripts/desktop-build.sh windows/amd64 <版本>
   ```
   - 缺 `~/go/bin` → `wails: command not found` EXIT=127；
   - 缺 reasonix-nsis → `makensis not found`（NSIS 不在 Program Files，专用副本在 `AppData/Local/reasonix-nsis/nsis-3.12/Bin`）。
9. **构建后验证**：`staging/version.txt` == 新版本号；「本轮结果」无红旗行（`no versioned InstallRoot found`=staging 未铺）。
10. **装机**：`restart_update` → `set_target "staging"` → `execute`（排队到回合边界）→ 重启后三证（list_versions active / current.json / versions 目录 mtime）+ 近 2h 日志零 FATAL。

### 阶段 3 · 推送与 CI 发布
11. **commit + push**：显式路径 add（禁 `git add -A`）；push 报 443 失败带 `-c http.proxy=http://127.0.0.1:40808`。
12. **推 tag**：`git tag desktop-v<版本> && git push origin desktop-v<版本>`。
13. **触发 CI**：`gh workflow run "Release fork desktop" -f tag=desktop-v<版本>`（workflow_dispatch，多平台构建）→ `gh run list --workflow release-fork.yml` 盯到 success。
    ⚠️ gh 默认可能解析到**上游仓库**——不带 `-R` 时必须在 fork 仓库目录内执行。

### 阶段 4 · 公告与收尾
14. **discussion 公告**：更新 fork 仓库 `discussion #95`（稳定性/发版公告位）为已修复+新版本（参照 #57 格式：一句话亮点+链接 notes/issue）。
15. **issue 状态更新**：本版修复对应的 issue（如 #94）评论进展并按需关闭/改状态；公告类 issue 创建后自行 `gh issue edit <N> --add-pin`。
16. **出包台账**：`docs/reasonix/handoff/fork开发-出包台账-<日期>.md` 追加本包条目（版本/基线/三证/内容清单）。
17. **发布后核验**：gh run list 确认 `Release fork desktop` success + `Deploy docs site` success（changelog 已上线）。

---

## 二、注意事项（踩坑集，新增坑随手补这里）

| # | 坑 | 处置 |
|---|---|---|
| 1 | 发版级 notes 基线写成上一时间戳包 → 大特性全漏 | 基线=**上一个发布的版本**（WRITING-GUIDE §1，2026-09-21 实锤） |
| 2 | FORK-vs-upstream.md 忘加本版表 → release-fork workflow **直接失败** | 阶段 1 第 5 步必做，构建前自查 grep 到本版号 |
| 3 | notes 文件名与包版本不一致（分钟级）→ workflow 精确名找不到，剥时间戳回退仍找不到则报错退出 | 文件名=`FORK-v1.38.3-YYYYMMDD-HHMM.md`，与 `desktop-build.sh` 传参**逐字一致** |
| 4 | 构建 `wails: command not found`（EXIT=127） | PATH 注入 `~/go/bin` |
| 5 | 构建 `makensis not found`（EXIT=1） | PATH 注入 `AppData/Local/reasonix-nsis/nsis-3.12/Bin`（本机 NSIS 不在 Program Files） |
| 6 | build 启动后才滚合的 commit 不进本包 | 先合完再构建；build 后滚的登记「待下包」 |
| 7 | changelog 头部统计行忘更新（本页 N 个包） | 改条目时同步改计数行；vtag 区分 内部包/正式 Release |
| 8 | gh 命令解析到上游仓库（esengine） | 带 `-R Linearl/DeepSeek-Reasonix` 或在 fork 仓库目录内跑 |
| 9 | git push 443 端口失败 | `-c http.proxy=http://127.0.0.1:40808`（gh API 通而 git 不通时同用） |
| 10 | 上游已于 2026-11-12 停止维护 | 不再向上游提 issue/PR 对照；动向巡检=`git fetch upstream` + `git log upstream/studio` |
| 11 | README 告示长期挂着 | 问题修复即**整段删除**，不换「已修复」版续挂 |
| 12 | locale 文案改动 | 必跑 `pnpm build` 过 bundle 棘轮（gzip 与 vite 报值口径不一致） |
| 13 | 测试断言绑死文案 | 改 locale 文案先 `git grep` 旧文案全仓（含 __tests__ 断言与注释）一起改 |

## 三、快速对照（TL;DR）

```
盘点基线 → notes + vs-upstream表 + changelog + README
  → desktop-build.sh（PATH 注入 wails+NSIS）→ staging 校验 → 装机三证
  → push（40808代理）→ tag desktop-v<版本> → gh workflow run "Release fork desktop"
  → discussion #95 / issue 公告 / 出包台账 / 双 workflow success 核验
```
