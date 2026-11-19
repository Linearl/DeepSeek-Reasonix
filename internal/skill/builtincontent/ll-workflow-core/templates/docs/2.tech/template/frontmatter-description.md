---
description: "文档 Frontmatter description 模板文档"
update_time: 2026-07-01
---
# 文档 Frontmatter 模板（description）

> 用途：为文档提供机器可读简介，供脚本自动生成 `docs/INDEX.md` 时作为摘要优先来源。

## 推荐写法

```yaml
---
description: "一句话说明本文用途、适用对象与主要内容"
---
```

## 示例

```markdown
---
description: "发布前检查清单，覆盖构建、测试、脚本与回滚核对项"
---

# 发布前检查清单（dev_xx）
```

## 规范建议

1. `description` 控制在 20~60 字，避免过长。
2. 以“这篇文档解决什么问题”为核心，不写空泛描述。
3. 若文档无 frontmatter，自动索引脚本会回退读取首个 H1 标题。
4. 对外文档建议统一提供 `description`，提升索引质量与检索命中率。
