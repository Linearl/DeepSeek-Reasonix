# mimo-builtin-skills（来源稿，勿直接改）

这三份 playbook **已内置进二进制**：`internal/skill/builtincontent/{deep-research,data-analytics,memory-search}/SKILL.md`，由 `go:embed` 随安装包分发 —— **用户不需要自行安装**，它们会直接出现在 skills catalog 里（任务 116，提交 `f94f410f0`）。

本目录保留的是 **MiMo-Code 移植时的来源稿**，仅作溯源与对照：

| 来源稿 | 运行时使用的内置版 |
|---|---|
| `deep-research/SKILL.md` | `internal/skill/builtincontent/deep-research/SKILL.md` |
| `data-analytics/SKILL.md` | `internal/skill/builtincontent/data-analytics/SKILL.md` |
| `memory-search/SKILL.md` | `internal/skill/builtincontent/memory-search/SKILL.md` |

**两者不再保持同步**。要改这几个技能的行为，改 `internal/skill/builtincontent/` 下的版本（并重出包）；本目录只在追溯移植来源时才需要读。

内置版相对来源稿的差异：补了 frontmatter（`name` / `description` / `runAs: inline`）—— 没有 description 会在 catalog 里显示成空行，且无法参与 auto-use 分类。
