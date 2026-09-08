#!/usr/bin/env python3
"""One-shot i18n key fixer for the v1.38.1 merge.

Runs tsc, collects every missing i18n key reported as TS2345, resolves copy
from the HEAD locale (pre-merge fork values), generates a Title-Case fallback
for keys that never existed, and appends them to the three locale files.
Idempotent: keys already present are skipped.
"""
import io
import json
import os
import re
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
FE = os.path.join(REPO, "desktop", "frontend")
TSC = os.path.join(FE, "node_modules", ".bin", "tsc.cmd")
LOCALES = {
    "zh": os.path.join(FE, "src", "locales", "zh.ts"),
    "en": os.path.join(FE, "src", "locales", "en.ts"),
    "zh-TW": os.path.join(FE, "src", "locales", "zh-TW.ts"),
}


def head_locale(lang):
    out = subprocess.run(
        ["git", "show", f"HEAD:desktop/frontend/src/locales/{lang}.ts"],
        capture_output=True, text=True, cwd=REPO, encoding="utf-8", errors="replace",
    )
    return out.stdout if out.returncode == 0 else ""


def run_tsc_missing():
    out = subprocess.run([TSC, "--noEmit"], capture_output=True, text=True,
                         cwd=FE, encoding="utf-8", errors="replace")
    keys = set()
    for m in re.finditer(r'error TS2345: Argument of type \'("[a-zA-Z]+\.[a-zA-Z.0-9]+")\'', out.stdout):
        keys.add(m.group(1).strip('"'))
    return sorted(keys)


def title_case(seg):
    return " ".join(w.capitalize() for w in re.split(r"[-_]", seg))


def main():
    head_zh = head_locale("zh")
    head_en = head_locale("en")
    missing = run_tsc_missing()
    print(f"missing keys: {len(missing)}")
    for lang, head_src in (("zh", head_zh), ("en", head_en), ("zh-TW", head_locale("zh-TW"))):
        path = LOCALES[lang]
        s = io.open(path, encoding="utf-8", newline="").read()
        closes = [m.start() for m in re.finditer(r"\n\}", s)]
        if not closes:
            print(path, "NO closing brace — skip")
            continue
        added = 0
        lines = []
        for key in missing:
            full = f'"{key}"'
            if full + ":" in s:
                continue
            seg = key.split(".")[-1]
            m = re.search('"' + re.escape(key) + r'": "([^"]*)"', head_src)
            val = m.group(1) if m else title_case(seg)
            lines.append((key, val))
        # insert all after the last key-value line before final closing brace
        if lines:
            insert_at = closes[-1] + 1
            block = "\n".join(
                f'  "{k}": {json.dumps(v, ensure_ascii=False)},' for k, v in lines
            ) + "\n"
            s = s[:insert_at] + block + s[insert_at:]
            added = len(lines)
        io.open(path, "w", encoding="utf-8", newline="").write(s)
        print(f"{lang}: inserted {added}")
    # summary
    for lang, path in LOCALES.items():
        s = io.open(path, encoding="utf-8", newline="").read()
        left = sum(1 for k in missing if f'"{k}"' not in s)
        print(f"{lang}: missing remaining {left}")


if __name__ == "__main__":
    main()
