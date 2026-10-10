# AI 安审试运行方案（任务 219 P1，2026-10-08 落盘，未启用）

状态：**workflow 草案已落盘于分支 `wt-219`
（`.github/workflows/ai-security-review.yml`），推送远端待用户确认**。
试运行采用任务书候选 A：`anthropics/claude-code-security-review`。

## 候选二选一结论

| | A：claude-code-security-review（Anthropic 官方，6.3k⭐） | B：pr-agent（Qodo 开源版，13.1k⭐） |
|---|---|---|
| 接入成本 | 单文件 GitHub Action，密钥=一个 API key | 自托管后端 + webhook + 自有 LLM key，或用 Qodo 托管服务 |
| 数据流 | PR diff → Anthropic API | 自托管可全自控（DeepSeek/MiMo）；托管版走 Qodo |
| 与现状契合 | 与现有 gh/Claude 数据流一致 | 需额外运维一个常驻服务 |
| 试运行可逆性 | 删一个文件即回滚 | 下线服务 + 删 webhook |

**选 A**，理由：试运行的目标是「用最小成本拿到噪音率与命中质量数据」，
A 一步到位；且本仓库是**公共 fork**（代码本就公开），diff 发往
Anthropic API 不构成新增暴露面。B 的「数据流自控」优势对私有仓库才
成立，若日后转私有或有敏感分支再评估 B（届时 pr-agent 仍在候选池）。

## 落地形态（草案要点）

- **label 门控试运行**：workflow 挂在 `pull_request` 上，但 job 级
  `if: contains(labels, 'ai-review')` ——只有打了 `ai-review` 标签的
  PR 才跑。不加标签的 PR 零成本零噪音，试运行对日常 PR 无影响。
- action 按 **main 分支 SHA `0c6a49f1`（2026-10-08）固定**——该项目
  不发 release tag，@main 裸引用有供应链风险，按 SHA 锁定，更新时
  有意 re-pin。
- `comment-pr: true`（发现以 PR 评论呈现，便于逐条人工分诊）；
  `upload-results: true`（审阅 JSON 存为工件，供噪音率统计）。
- 前置条件（**用户步骤，本轮未做**）：在仓库 secrets 配置
  `ANTHROPIC_API_KEY`（需开通 Claude Code 的 API key）。secret 缺失时
  仅打了标签的 PR 会失败，其余 PR 不受影响。

## 试运行协议

1. 推送分支、合并进 main-v2-stable 后，配置 `ANTHROPIC_API_KEY` secret。
2. 挑 3 个以上近期真实 PR（含至少 1 个安全相关改动，如鉴权/worker 输入
   处理），打 `ai-review` 标签触发。
3. 每个命中逐条人工分诊，记入下表：

| PR | 命中数 | 真实问题 | 误报 | 备注 |
|---|---|---|---|---|
| | | | | |

4. 汇总两个指标：**噪音率 = 误报/命中总数**；**命中质量** = 是否抓到
   人工复查时确认的真实问题（或至少指向值得看的代码位置）。

## 去留判据（预先定死，防止事后合理化）

- **留**：噪音率 ≤ 50% 且至少 1 次抓到真实问题或高价值线索；
- **留但改配置**：噪音偏高但有真实捕获 → 收紧
  `false-positive-filtering-instructions` / 限定扫描范围后再测 3 个 PR；
- **去**：噪音率 > 50% 且零真实捕获 → 删 workflow 文件，P1 结束，
  结论记回任务清单（候选 B 不再自动跟进，需另立项）。

## 已知限制（试运行期间接受）

- action 未对 PR 内容中的提示注入做加固（上游 README 原文）。当前 PR
  全部来自自有分支，可接受；**外部贡献者可开 PR 前必须重新评估**。
- 每次打标签/推 commit 跑一次 Claude 审阅，模型默认 Opus 级，单个 PR
  成本量级为美元级——试运行 PR 数量控制在个位数。
- 回滚 = 删除 `.github/workflows/ai-security-review.yml` 一个文件；
  不留任何仓库内残留。
