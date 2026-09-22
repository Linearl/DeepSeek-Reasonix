# Gitleaks CI 集成方案（任务 219 P0，2026-09-22 定稿，未启用）

状态：**方案文档，workflow 未落地**。启用需要把下文 YAML 放进
`.github/workflows/gitleaks.yml` 并走 worktree 分支推送，推送前用户确认
（任务 219 边界纪律）。

## 基线（2026-09-22，gitleaks 8.30.1）

- 全量历史扫描：9701 commits / ~1.23 GB / 1m21s。
- 原始命中 415（generic-api-key 395 / jwt 18 / private-key 2）。
- 逐类核查结论：全部为测试 fixture 或历史误报——测试运行时自造的 RSA
  keypair（crash-report firebase 测试）、`ServeCapsToken` 版本标识串
  （`internal/remote/bootstrap/launch.go`）、上游历史文件里的内置默认 key
  （`src/config.ts` Metaso，文件已不存在于 HEAD）等。**无真实密钥泄漏**。
- 误报已按「逐例核查」原则写入根目录 `.gitleaks.toml` allowlist（路径 +
  一条 regex），allowlist 后全量重扫 **no leaks found**。后续改 allowlist
  必须附已核查的例子，禁止整类静音。

## 本地接入（已启用，零 CI 改动）

- `scripts/gitleaks-check.sh`：
  - 默认（staged）：`gitleaks protect --staged --redact --verbose`，提交前跑。
  - `full`：`gitleaks detect --source . --redact`，全历史复核。
  - gitleaks 缺失时 exit 2 并给出安装指引；安装方式见脚本头注释
    （release 单二进制即可，`GITLEAKS_BIN` 可指定路径）。
- 与既有 `scripts/credential-leak-check.mjs` 互补：那个管「测试运行时打印
  了真实凭据」（动态），gitleaks 管「仓库内容/历史里有密钥形态的字串」
  （静态）。

## CI 接入（待用户确认后启用）

推荐 job（只在 PR 与 main-v2-stable 推送时跑全量 detect；单文件成本可忽略）：

```yaml
name: gitleaks
on:
  pull_request:
  push:
    branches: [main-v2-stable]
jobs:
  gitleaks:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0   # 全历史扫描需要完整 clone
      - uses: gitleaks/gitleaks-action@v2
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

要点：
- `gitleaks-action@v2` 读取仓库根 `.gitleaks.toml`，本地与 CI 同一份规则，
  不产生两边漂移。
- `fetch-depth: 0` 必须保留：增量扫描会漏掉历史中的密钥。
- 启用后首个 PR 若有新命中，处理流程 = 修掉或（确属误报时）在
  `.gitleaks.toml` 附核查说明加 allowlist 条目；**不静默忽略**。
- 不启用 scan-on-push 全分支矩阵（成本无谓）；history 扫描由 monthly
  手动 `scripts/gitleaks-check.sh full` 兜底即可。
