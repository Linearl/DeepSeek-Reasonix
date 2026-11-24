#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""tl1742: batch-report for provider content_filter rejection case files.

Reads the JSON case files the agent's side-channel capture writes to
<stateRoot>/rejection-cases/ (schema_version 1, see
internal/agent/provider_rejection_capture.go) and renders a markdown summary:
case list + outcome distribution + shape features (language mix, top terms,
quotation density) + rewrite-round pass comparison. Zero dependencies.

Usage:
    python rejection_cases_report.py [--dir DIR] [--out FILE] [--limit N]

Default dir: %APPDATA%/reasonix/rejection-cases (Windows) or
~/.config/reasonix/rejection-cases elsewhere.
"""

import argparse
import collections
import json
import os
import re
import sys
from datetime import datetime, timezone

# Words that carry no clustering signal: English stopwords plus every word of
# the fixed rejection template and the host ladder instructions (those are
# identical in every case and would otherwise dominate the top terms).
STOPWORDS = {
    "the", "a", "an", "and", "or", "of", "to", "in", "is", "it", "was",
    "for", "on", "with", "as", "by", "at", "from", "this", "that", "be",
    "are", "not", "but", "if", "into", "your", "you", "we", "our", "they",
    "their", "its", "will", "can", "may", "do", "does", "did", "so", "than",
    "then", "there", "here", "no", "yes", "only", "also", "very", "more",
    "most", "just", "one", "two", "request", "rejected", "answer", "reply",
    "provider", "safety", "layer", "round", "ladder", "content", "model",
    "rewrite", "rewritten", "rejection", "recovery", "instruction", "fixed",
    "sentence", "considered", "high", "risk", "instead", "because", "before",
    "after", "once", "file", "full", "short", "long", "make", "send", "get",
    "when", "what", "which", "how", "why", "out", "up", "down", "over",
}
TEMPLATE_PATTERNS = [
    "request was rejected", "considered high risk", "safety layer",
    "recovery round", "rewrite the answer", "file-write tool", "fixed rejection",
]

CJK_RANGE = re.compile(r"[\u4e00-\u9fff]")
ENGLISH_WORD = re.compile(r"[a-zA-Z][a-zA-Z0-9_]{2,}")
CJK_BIGRAM = re.compile(r"[\u4e00-\u9fff]{2}")


def default_dir():
    if sys.platform == "win32" and os.environ.get("APPDATA"):
        return os.path.join(os.environ["APPDATA"], "reasonix", "rejection-cases")
    cfg = os.environ.get("XDG_CONFIG_HOME") or os.path.expanduser("~/.config")
    return os.path.join(cfg, "reasonix", "rejection-cases")


def load_cases(case_dir):
    """Returns (cases, skipped) where skipped counts unreadable files."""
    cases, skipped = [], 0
    if not os.path.isdir(case_dir):
        return cases, skipped
    for name in sorted(os.listdir(case_dir)):
        if not name.endswith(".json"):
            continue
        path = os.path.join(case_dir, name)
        try:
            with open(path, "r", encoding="utf-8") as f:
                raw = json.load(f)
        except (OSError, ValueError):
            skipped += 1
            continue
        if not isinstance(raw, dict) or raw.get("schema_version") != 1:
            skipped += 1
            continue
        raw["_file"] = name
        cases.append(raw)
    cases.sort(key=lambda c: c.get("created_at", ""))
    return cases, skipped


def cjk_ratio(text):
    if not text:
        return 0.0
    runes = [c for c in text if not c.isspace()]
    if not runes:
        return 0.0
    return sum(1 for c in runes if CJK_RANGE.match(c)) / len(runes)


def dominant_language(text):
    return "zh" if cjk_ratio(text) > 0.30 else "en"


def trigger_of(case):
    """The trigger user message lives inside context_before in the schema."""
    return (case.get("context_before") or {}).get("trigger_user_message") or ""


def top_terms(cases, limit=15):
    """Frequency of content terms in trigger messages + rejected replies.
    Chinese contributes adjacent-character bigrams; English lowercase words.
    The fixed template / instruction vocabulary is excluded (identical in
    every case, zero clustering signal)."""
    counter = collections.Counter()
    for c in cases:
        texts = [trigger_of(c)]
        for r in c.get("rounds") or []:
            texts.append(r.get("rejected_reply") or "")
        for text in texts:
            low = text.lower()
            for pat in TEMPLATE_PATTERNS:
                low = low.replace(pat, " ")
            counter.update(CJK_BIGRAM.findall(low))
            counter.update(w for w in ENGLISH_WORD.findall(low)
                           if w.lower() not in STOPWORDS)
    return counter.most_common(limit)


def quotation_density(cases):
    """Shape features used to test the 'shape sensitivity' hypothesis: heavy
    quoting / code blocks are the classic filter triggers."""
    agg = collections.Counter()
    for c in cases:
        for r in c.get("rounds") or []:
            b = r.get("rejected_reply") or ""
            agg["code_fence_pairs"] += b.count("```") // 2
            agg["double_quote_pairs"] += b.count('"') // 2
            agg["cjk_quote_pairs"] += b.count("「") + b.count("『") + b.count("“")
            agg["markdown_quote_lines"] += len(
                [ln for ln in b.splitlines() if ln.lstrip().startswith(">")])
            agg["list_lines"] += len(
                [ln for ln in b.splitlines()
                 if re.match(r"\s*([-*+]|\d+\.)\s", ln)])
    return agg


def parse_ts(ts):
    if not ts:
        return None
    try:
        return datetime.fromisoformat(ts.replace("Z", "+00:00"))
    except ValueError:
        return None


def fmt_len(text):
    return str(len(text or ""))


def render(cases, skipped, case_dir, limit):
    out = []
    out.append("# Provider content_filter 拦截案例汇总（tl1742）")
    out.append("")
    out.append("- 生成时间: %s" % datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"))
    out.append("- 案例目录: `%s`" % case_dir)
    out.append("- 案例数: %d（跳过不可解析文件 %d 个）" % (len(cases), skipped))
    if not cases:
        out.append("")
        out.append("目录内暂无案例文件。")
        return "\n".join(out)

    # ---- 概览 ----
    outcomes = collections.Counter(c.get("outcome") or "pending" for c in cases)
    sessions = set(c.get("session_ref") or "?" for c in cases)
    providers = collections.Counter(c.get("provider") or "?" for c in cases)
    models = collections.Counter(c.get("model_ref") or "?" for c in cases)
    created = [parse_ts(c.get("created_at")) for c in cases]
    created = [d for d in created if d]
    out.append("")
    out.append("## 概览")
    out.append("")
    out.append("- outcome 分布: %s" % ", ".join(
        "`%s`=%d" % (k, v) for k, v in sorted(outcomes.items())))
    out.append("- 涉及会话（transport ref）: %d 个" % len(sessions))
    out.append("- provider 分布: %s" % ", ".join(
        "`%s`=%d" % (k, v) for k, v in providers.most_common()))
    out.append("- model 分布: %s" % ", ".join(
        "`%s`=%d" % (k, v) for k, v in models.most_common()))
    if created:
        out.append("- 时间范围: %s ~ %s (UTC)" % (
            min(created).strftime("%Y-%m-%d %H:%M:%S"),
            max(created).strftime("%Y-%m-%d %H:%M:%S")))

    # ---- 案例清单 ----
    out.append("")
    out.append("## 案例清单")
    out.append("")
    out.append("| # | created_at | provider | model | outcome | rounds | session | B长度 | D长度 | 触发消息摘要 | 文件 |")
    out.append("|---|-----------|----------|-------|---------|--------|---------|-------|-------|--------------|------|")
    for i, c in enumerate(cases[:limit] if limit else cases, 1):
        rounds = c.get("rounds") or []
        trigger = trigger_of(c).replace("|", "\\|").replace("\n", " ")
        if len(trigger) > 40:
            trigger = trigger[:40] + "…"
        out.append("| %d | %s | %s | %s | `%s` | %d | `%s` | %s | %s | %s | `%s` |" % (
            i,
            (c.get("created_at") or "?")[:19],
            c.get("provider") or "?",
            c.get("model_ref") or "?",
            c.get("outcome") or "pending",
            len(rounds),
            c.get("session_ref") or "?",
            fmt_len((rounds[0].get("rejected_reply") if rounds else "") or ""),
            fmt_len(c.get("rewritten_reply") or ""),
            trigger or "（空）",
            c["_file"],
        ))
    if limit and len(cases) > limit:
        out.append("")
        out.append("（清单截断：共 %d 条，仅显示前 %d 条）" % (len(cases), limit))

    # ---- 形态特征摘要（聚类/对照的输入） ----
    out.append("")
    out.append("## 形态特征摘要")
    out.append("")
    langs = collections.Counter(
        dominant_language(trigger_of(c)) for c in cases)
    out.append("- 触发消息语言分布: 中文主导 %d / 英文主导 %d" % (
        langs.get("zh", 0), langs.get("en", 0)))
    terms = top_terms(cases)
    if terms:
        out.append("- 高频词/词对 top %d: %s" % (
            len(terms), ", ".join("`%s`(%d)" % (t, n) for t, n in terms)))

    quote = quotation_density(cases)
    out.append("- 被拒回复（B 段）引用密度累计: 代码块 %d 对 / 双引号 %d 对 / CJK 引号 %d 处 / markdown 引用行 %d / 列表行 %d" % (
        quote["code_fence_pairs"], quote["double_quote_pairs"],
        quote["cjk_quote_pairs"], quote["markdown_quote_lines"],
        quote["list_lines"]))

    # ---- 改写轮对照（形态敏感假设的读数） ----
    out.append("")
    out.append("## 改写轮对照")
    out.append("")
    ratios = []
    for c in cases:
        d = c.get("rewritten_reply") or ""
        rounds = c.get("rounds") or []
        b = (rounds[0].get("rejected_reply") or "") if rounds else ""
        if d and b:
            ratios.append(len(d) / len(b))
    out.append("- 改写轮通过（rewrite_passed）: %d" % outcomes.get("rewrite_passed", 0))
    out.append("- 降级轮通过（degrade_passed）: %d" % outcomes.get("degrade_passed", 0))
    out.append("- 阶梯耗尽（exhausted）: %d" % outcomes.get("exhausted", 0))
    out.append("- 工具轮重置 streak（streak_reset_by_toolwork）: %d" % outcomes.get("streak_reset_by_toolwork", 0))
    if ratios:
        out.append("- D/B 长度比（改写后回复 / 首次被拒回复）: 平均 %.2f，中位 %.2f，样本 %d" % (
            sum(ratios) / len(ratios),
            sorted(ratios)[len(ratios) // 2],
            len(ratios)))
        passed = outcomes.get("rewrite_passed", 0) + outcomes.get("degrade_passed", 0)
        out.append("- 同一内容换措辞即通过 = 形态敏感候选；通过率 %d/%d" % (
            passed, len(cases)))
    else:
        out.append("- 无带 D 段的案例，改写对照读数不可用")
    return "\n".join(out)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dir", default=default_dir(),
                        help="case files directory (default: state root rejection-cases)")
    parser.add_argument("--out", default="", help="write markdown to FILE instead of stdout")
    parser.add_argument("--limit", type=int, default=0,
                        help="cap the case-list table rows (0 = all)")
    args = parser.parse_args()

    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(encoding="utf-8", errors="replace")

    cases, skipped = load_cases(args.dir)
    report = render(cases, skipped, args.dir, args.limit)
    if args.out:
        with open(args.out, "w", encoding="utf-8") as f:
            f.write(report + "\n")
        print("report written: %s (%d cases)" % (args.out, len(cases)))
    else:
        print(report)


if __name__ == "__main__":
    main()
