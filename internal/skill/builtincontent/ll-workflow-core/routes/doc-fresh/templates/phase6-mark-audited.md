---
description: "文档保鲜审计——Phase 6 标记已审计操作说明"
update_time: 2026-07-01
---
# Phase 6: 标记已审计

## 用途

审计汇总完成后，对每个已审计文档执行 `--mark-audited`，将当前 SHA 写入 `audited_sha` + `audited_at`。下次扫描时，同 SHA 且 TTL 窗口内的文档自动跳过。

## 操作

### 单个标记

```bash
python 04-auto_audit/scripts/check-doc-freshness.py --mark-audited "2.tech/api/01-前端API.md"
```

### 批量标记

Phase 5 完成后，对 `scan-result.json` 中 `audit_files` 列表的每个文件执行：

```bash
cd <repo_root>
for doc in <audited_file_list>; do
  python 04-auto_audit/scripts/check-doc-freshness.py --mark-audited "$doc"
done
```

## 效果

标记前：
```json
{
  "2.tech/api/01-前端API.md": {
    "git_sha": "abc123",
    "audited_sha": null,
    "audited_at": null
  }
}
```

标记后：
```json
{
  "2.tech/api/01-前端API.md": {
    "git_sha": "abc123",
    "audited_sha": "abc123",
    "audited_at": "2026-05-29T12:00:00.000000+00:00"
  }
}
```

## 去重 TTL 行为

| 条件 | 下次扫描结果 |
|------|-------------|
| SHA 不变 + audited_at < 14d | skipped（去重） |
| SHA 不变 + audited_at >= 14d | new（TTL 过期，重审） |
| SHA 变更 | new（文档已修改） |
| `--force-full` | 忽略去重，全量 new |

## 配置控制

在 `document-freshness.yaml` 中：

```yaml
dedup:
  enabled: true       # 关闭 = 每次全量审计
  ttl_days: 14        # 0 = 永久跳过（除非 SHA 变）
```

## 验证

标记后可查看 state.json 确认：

```bash
python -c "import json; d=json.load(open('04-auto_audit/config/state.json')); print({k:v.get('audited_sha','')[:8] for k,v in d['entries'].items() if v.get('audited_sha')})"
```
