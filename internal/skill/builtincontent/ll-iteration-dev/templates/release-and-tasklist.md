# 发版链与 tasklist 回写（详版）· B4 外移

> `SKILL.md` §5.7 只留判据与指针；**逐条命令与台账纪律**在此。
> 命中 §5.7 某条时来这里照做。

---

## §5.7.0 出包前测试文档（2026-09-22 用户要求，必做）

最终出包前，主会话必须撰写一份**面向用户的验证文档**（放 `tasks/` 或台账内嵌）：

- **改动点清单**：本批每个用户可见改动一条（功能名 + 一句话说明 + 触发路径）
- **验证方法**：每条改动给出具体验证步骤（哪里点、期待看到什么、日志/读数在哪查）
- **已知未含项（v3 必列）**：被 §5.8 踢出范围的线及其未完成任务，逐条列出

用途：用户装机后照单测试；**缺此文档不得出包**。

---

## §5.7.1 发版链（版本号与台账纪律）

- **版本号不自增**：复用 fork 当前版本号（现为 `1.38.3`），包名带构建时间戳 `1.38.3-YYYYMMDD-HHMM`；`versionDirRE` 只收 `-` 后缀。
- **release notes 先于构建**：增量基线 = 上一时间戳包，**只写增量不堆叠历史**；文件与包版本同名。
- **正式发版**：`release-fork.yml` workflow 手动 dispatch + 传 tag（`desktop-v1.38.3-<时间戳>`）；workflow 会找 `release-notes/FORK-v<基线版本>.md`（时间戳剥掉）并**硬性要求**引用 `FORK-vs-upstream.md`——发版前台账必须先加本版表。
- **出包**：用户明确要求才出（**不主动构建安装包**）。
  ```bash
  bash scripts/build-local-installer.sh
  ```
  **不要用 wails `-nopackage`**；构建前 `TestDesktopRenderTableCoversEveryKey` 必须绿。
- **台账留档（每次必做）**：`handoff/fork开发-出包台账-*.md` 追加一节，含：
  出包时间 / 版本 / HEAD / 产物字节数 / 相对上一包新增提交 / 已知未含项 / 验证状态 / 用户测试结论。
- **不自动合并主线**：落地默认保留分支 + 交分支名，合并由用户指定会话执行。

---

## §5.7.2 人机协同验证（证据三件套 + 基线对照）

- **证据三件套**（缺一不算验过）：**现象** + **日志**（时间戳片段）+ **截图**。
  修复类必须给「改前 vs 改后」对照。
- **基线对照铁律**：宣布复现或修复前，**同一条命令在改动前后两侧都跑一遍**——
  合成会话 bench 测不到 app 级运行时契约。
- **性能类**：用 `scripts/perf-probe/`。
- **内存类**：第一工具是 **heap profile**，不是版本二分。
- **装机验证是硬闸**：包交用户装机 → 用户实测反馈（现象+日志+截图）→ 才算闭环。
- **任务正文的验收条目就是对账单**：阶段 2 写的可证伪验收，逐条勾；
  **勾不了的写「未验 + 为什么」**，不许留空。

---

## §5.7.3 回写 tasklist（脚本路径，禁手搬）

```bash
cd docs/tasklist
python scripts/tasklist_promote.py NNN --dry-run    # 先预览（仅 done 可迁）
python scripts/tasklist_promote.py NNN              # 迁移：正文整块搬入已完成文件 + 待办侧留指针 + 总表三处同步
python scripts/tasklist_promote.py --check          # 一致性体检
python scripts/tasklist_db.py scan                  # 重建派生索引（必跑）
python scripts/tasklist_db.py sql "SELECT num,status FROM tasks WHERE num=NNN"   # 逐个校验
```

### 标记写法（⚠️ 写错 = 状态静默不生效）

| 状态 | 写法 |
|---|---|
| done | `✅` 必须在「任务」**之前**；且「日期 + 完成」需**紧邻** |
| partial | 用「剩余仅」；`done_at` 留空是**正常的** |
| watching | 用 `⏳ 观察中` |
| blocked | 用 `⏸ 阻塞` |

### 其它纪律

- **回写依据是提交，不是记忆**：`git log --oneline | grep "task N"` → `git merge-base --is-ancestor <sha> HEAD` 确认已进当前分支；自称 `slice`/`A级` 的按 `partial` 处理。
- **v3：被踢出范围的线不得标 `done`**，按实情标 `partial`（剩「超轮次未通过」）或 `⏸ 阻塞`。
- **提交用显式路径**（禁 `-A`/`.`）；并行会话共用仓库时先 `git status`。
- **tasklist 只由主会话统一更新**（写进派活禁项，避免并行冲突）。

---

## §5.7.4 收尾归档

- handoff 自包含（`<会话名>-<主题>-<YYYYMMDD>.md`；3 天保留策略）。
- 一次性测量/诊断脚本归档到 `scripts/<主题>/`，不要留在 `tmp/`。
- 清理顺序见 `pitfalls.md` 第 1、2 条：
  **先删 junction（必须验证成功）→ `git worktree remove --force` → `git branch -D`**；
  磁盘紧张时 `go clean -cache`。
- 本轮结论若要复用 → 写记忆（技术判断 `activation=relevant`）；流程类写进本 skill。
