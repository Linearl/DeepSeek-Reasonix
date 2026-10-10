# Gitleaks CI 集成方案（任务 219 P0，2026-09-22 定稿；2026-10-08 workflow 落盘）

状态：**workflow 已落盘于分支 `wt-219`（`.github/workflows/gitleaks.yml`），
推送远端待用户确认**（任务 219 边界纪律）。本文档记录落地版相对 9/22
草案的差异，以及推送后的启用核对清单。

## 基线（2026-09-22，gitleaks 8.30.1；2026-10-08 复核延展）

- 全量历史扫描：9701 commits / ~1.23 GB / 1m21s。
- 原始命中 415（generic-api-key 395 / jwt 18 / private-key 2）。
- 逐类核查结论：全部为测试 fixture 或历史误报——测试运行时自造的 RSA
  keypair（crash-report firebase 测试）、`ServeCapsToken` 版本标识串
  （`internal/remote/bootstrap/launch.go`）、上游历史文件里的内置默认 key
  （`src/config.ts` Metaso，文件已不存在于 HEAD）等。**无真实密钥泄漏**。
- 误报已按「逐例核查」原则写入根目录 `.gitleaks.toml` allowlist（路径 +
  一条 regex），allowlist 后全量重扫 **no leaks found**。后续改 allowlist
  必须附已核查的例子，禁止整类静音。
- **2026-10-08 复核**（12824 commits / 1.32 GB / 1m47s，同版本 8.30.1）：
  新增 8 个命中，全部为 `testdata/` 会话录制 fixture 里的
  `IdempotencyKey` 字段（内部工具调用幂等回执摘要
  `recoveryDigest(keyJSON)`，非凭据；其中 6 处所在文件已删、仅存于
  历史）。已按同一纪律附例扩展 allowlist（2 条窄路径），扩展后全量
  重扫 **no leaks found**。

## 本地接入（已启用，零 CI 改动）

- `scripts/gitleaks-check.sh`：
  - 默认（staged）：`gitleaks protect --staged --redact --verbose`，提交前跑。
  - `full`：`gitleaks detect --source . --redact`，全历史复核。
  - gitleaks 缺失时 exit 2 并给出安装指引；安装方式见脚本头注释
    （release 单二进制即可，`GITLEAKS_BIN` 可指定路径）。
- 与既有 `scripts/credential-leak-check.mjs` 互补：那个管「测试运行时打印
  了真实凭据」（动态），gitleaks 管「仓库内容/历史里有密钥形态的字串」
  （静态）。

## CI 接入（workflow 已落盘，推送待确认）

落地文件：`.github/workflows/gitleaks.yml`（分支 wt-219）。**落地版相对
9/22 草案有三处修正**，均为必要而非偏好：

1. **v2 → v3（硬性）**：gitleaks-action v2 跑在 Node 20 上，GitHub 已于
   2026-09-16 彻底停跑 Node 20 action——9/22 草案的 v2 写法现在必然失败。
   v3 是官方接替版（官方声明：仅 Node 20→24 运行时变化，输入/输出/行为
   不变）。同时 checkout 用仓库现行的 v7（全仓 58 处统一 v7）。
2. **触发面**：`pull_request` + `push: [main-v2-stable]` + `workflow_dispatch`
   + **每周一一次全量 cron**。cron 把方案里「monthly 手动 `full` 兜底」
   升级为自动（公共仓库托管 runner，成本可忽略）——这是对草案的放宽，
   不想要可在推送前删掉 `schedule:` 块。
3. **报告工件**：`GITLEAKS_ENABLE_UPLOAD_ARTIFACT: "true"` ——有命中的
   run 自动上传 `gitleaks-report.sarif` 工件；零告警 run 无工件（job
   summary 即记录），这是 action 的内建语义。

**路径过滤的明确决策：触发层不做 paths-ignore**。密钥可以藏在任何文件
类型里（Markdown 报告恰是经典泄漏载体），按路径跳过扫描会制造盲区；
路径过滤只存在于 `.gitleaks.toml` 的 allowlist（逐例核查过的那种）。
另：gitleaks 目前不是 required check，无 ci.yml 注释里说的
「workflow 级 paths-ignore 卡 required checks」问题；若日后设为
required，沿用 ci.yml 的 changes-job 门控模式。

**许可注记**：`GITLEAKS_LICENSE` 仅组织账号仓库需要（个人账号免费）。
当前 fork 在个人账号下，无需配置；若仓库迁入组织，届时从 gitleaks.io
取免费 license 加为 secret。

### 启用核对清单（推送 = 把 wt-219 分支推到 origin；合并进 main-v2-stable 前先观察）

推送后预期行为（按顺序自验）：

1. `git push origin wt-219` → 因 workflow 文件已在分支上，push 事件
   **不**触发 gitleaks（push 触发限 main-v2-stable）；`pull_request`
   触发自建 PR 起。
2. 从 wt-219 向 main-v2-stable 开 PR：gitleaks job 触发，对 PR commit
   做扫描。**预期 pass**（基线零告警 + 本分支只加 workflow/文档）。
   同时 ai-security-review 出现（不自动跑，见 `docs/AI-REVIEW-TRIAL.md`）。
3. 手动验证全量路径：Actions → gitleaks → Run workflow（workflow_dispatch）
   → `fetch-depth: 0` 全历史扫描，**预期 no leaks found**（约 2-4 分钟，
   其中大头是 1.23 GB 全量 clone）。
4. 阴性路径验证（可选但推荐）：开一个测试 PR 塞入假密钥
   （如 `AWS_SECRET_ACCESS_KEY=aws` 格式的 fixture），预期 job 红 +
   PR 出现 gitleaks 评论 + run 页面出现 sarif 工件；验完关闭该 PR 不合并。
5. 上述全绿后合 PR 进 main-v2-stable：push 到默认分支触发全量扫描，
   此后每个 PR 与每周一自动跑。

告警处理流程（job 红 = 出现命中）：

1. 看 run 页面 job summary 与 sarif 工件定位命中（文件/行/规则/密钥形态，
   `--redact` 生效不会回显完整密钥）。
2. **判定**：真实密钥 → 立即作废轮换该凭据（轮换流程由用户执行），
   然后从历史中清除（filter-repo/BFG，另立任务）；确属误报 → 在
   `.gitleaks.toml` 加 allowlist 条目并**附已核查的例子**（本文件
   基线小节的既有纪律），重跑全量确认归零。
3. **不静默忽略**：不允许用「跳过 CI」「临时改规则不记录例子」的方式让
   job 变绿。

回滚路径（按成本从低到高）：

- **停触发**：Actions 页禁用 gitleaks workflow（可逆，秒级）；
- **删规则**：`git revert` 引入 gitleaks.yml 的 commit 或直接删该文件
  （本地门禁 `scripts/gitleaks-check.sh` 与 `.gitleaks.toml` 不受影响，
  保留）；
- 若因 GITLEAKS_LICENSE 报错（组织账号场景）：补 secret 即可，不必回滚。
