---
description: "ll-workflow-core 路由资源目录说明"
update_time: 2026-07-01
---
# Routes 目录说明

`routes/` 用于存放各个 workflow route 的专属资源。

当前已存在：

- `init/`
- `req/`
- `dev/`
- `aud/`

后续新增流程时，若需要专属模板、脚本、配置或参考文档，建议直接新增：

- `routes/<short_name>/`

典型子目录：

- `templates/`
- `references/`
- `docs/`
- `scripts/`
- `config/`

并在 `config/resource-bundles.yaml` 中为该 route 声明 bundle。
