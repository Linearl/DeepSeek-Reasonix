---
description: "将 ll-workflow-core 从 monorepo 子目录发布为独立 GitHub 仓库的步骤与检查清单"
update_time: 2026-07-01
---
# ll-workflow-core 发布指南

本文档面向“准备把 `ll-workflow-core` 单独发布到 GitHub”的场景。

## 1. 推荐发布模型

最简单的做法是：

**把当前目录 `.github/skills/ll-workflow-core/` 的内容，直接作为独立仓库根目录发布。**

也就是说，发布后的仓库根目录应直接包含：

- `SKILL.md`
- `README.md`
- `LICENSE.txt`
- `config/`
- `docs/`
- `references/`
- `routes/`
- `templates/`

不要把上层 `.github/skills/` 一起带出去，否则仓库根目录会多包一层，安装体验会比较别扭。

## 2. 发布前必须检查的内容

### 2.1 入口与元数据

- `SKILL.md` frontmatter 可正常解析
- `name` 与仓库目录名保持一致
- `description` 能覆盖真实触发场景
- `license` 已指向有效文件或 SPDX 标识

### 2.2 相对路径完整性

至少抽查以下文件中的相对路径是否仍然成立：

- `SKILL.md`
- `config/route-registry.yaml`
- `config/resource-bundles.yaml`
- `docs/README.md`

### 2.3 组织内向残留

发布前建议搜索并处理以下几类内容：

- 特定仓库名
- 绝对路径或本机路径
- 明显历史版本来源描述（例如只在原始母仓库内才成立的说明）
- 不应对外暴露的内部术语

说明：

- `dev_xx`、`0.version_plan / 1.task / 2.tech ...` 这类内容是当前技能设计的一部分，
  不是必须删除的“内部残留”；
- 但如果某段描述只是在说明“它最初从哪个项目抠出来”，通常应改成更中性的说法。

### 2.4 配置自洽性

确认三类核心配置仍互相对应：

- `config/directory-layout.yaml`
- `config/route-registry.yaml`
- `config/resource-bundles.yaml`

如果改了 profile 名称、bundle 名称或 workflow 路径，这三处通常要一起检查。

## 3. 建议的 smoke test

发布前至少做一轮最小安装验证。

### 场景 A：仓库级安装

1. 新建一个临时测试仓库
2. 把本仓库内容复制到 `.github/skills/ll-workflow-core/`
3. 打开 Copilot / agent 环境
4. 用以下表达测试是否能正确触发：
   - “使用 `ll-workflow-core` 执行 `init`”
   - “使用 `ll-workflow-core` 执行 `req`”
   - “使用 `ll-workflow-core` 执行 `dev`”
   - “使用 `ll-workflow-core` 执行 `aud`”

### 场景 B：用户级安装

1. 把本仓库内容复制到 `~/.github/skills/ll-workflow-core/`
2. 在一个干净工作区中测试相同触发表达
3. 确认没有依赖“母仓库上下文”才能生效的说明或路径

## 4. 版本化建议

建议使用语义化版本：

- `0.x`：仍在快速演进，结构与命名可能调整
- `1.x`：路由、bundle 和 profile 命名趋于稳定

每次发布前，建议至少记录：

- 新增/调整的 route
- 新增/调整的 bundle
- 目录 profile 的变更
- 对外兼容性变化

如果后续公开维护频率较高，可以再补 `CHANGELOG.md`。

## 5. 适合在发布前手动替换的占位内容

本仓库内存在少量模板级占位符，例如：

- `__REPO_NAME__`
- `dev_xx`
- `__VERSION__`

其中：

- `__REPO_NAME__` 适合在具体使用时填入目标项目名；
- `dev_xx` / `__VERSION__` 属于版本化模板设计的一部分，通常不必在仓库发布前统一替换。

## 6. 一个简单判断标准

如果把本目录整体复制到一个全新环境后，以下三件事都成立，就说明它已经基本具备独立发布条件：

1. 人能看懂：`README.md` 和 `docs/README.md` 能解释它是什么、怎么扩展；
2. 机能装上：`SKILL.md` 与相对路径都能正常工作；
3. 不靠母仓库：核心说明不再依赖“你必须知道它原本在哪个项目里”。
