#!/usr/bin/env python3
"""One-shot TS6133 (unused) import cleaner.

Runs tsc, collects every "declared but its value is never read" report for
import-bound identifiers, removes them from their import statements, drops
import lines that become empty, and loops until tsc stops reporting unused
imports. Non-import unused declarations are left for manual review.
"""
import io
import os
import re
import subprocess
import sys

FE = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "desktop", "frontend")
TSC = os.path.join(FE, "node_modules", ".bin", "tsc.cmd")


def run_tsc():
    out = subprocess.run([TSC, "--noEmit"], capture_output=True, text=True,
                         cwd=FE, encoding="utf-8", errors="replace")
    return out.stdout


def collect_unused(stdout):
    """Return {file: [identifier, ...]} for unused *import-bound* names only."""
    per_file = {}
    for m in re.finditer(r"^(src/[^\(]+\.tsx?)\((\d+),(\d+)\): error TS6133: '([^']+)' is declared but its value is never read\.", stdout, re.M):
        path, line, col, name = m.group(1), int(m.group(2)), int(m.group(3)), m.group(4)
        per_file.setdefault(path, []).append((line, col, name))
    return per_file


def remove_name_from_import_line(line, name):
    """Remove `name` from a named-import clause. Returns new line or None if not an import."""
    m = re.match(r"import \{(.*)\} from (.*);", line)
    if not m:
        return None
    names = [x.strip() for x in m.group(1).split(",")]
    kept = [x for x in names if x.replace("type ", "") != name]
    if len(kept) == len(names):
        return None
    if not kept:
        return ""  # whole line goes
    return "import { " + ", ".join(kept) + " } from " + m.group(2) + ";"


def main():
    for round_no in range(1, 6):
        stdout = run_tsc()
        per_file = collect_unused(stdout)
        if not per_file:
            print(f"round {round_no}: no unused imports left")
            break
        total = sum(len(v) for v in per_file.values())
        print(f"round {round_no}: {total} unused imports in {len(per_file)} files")
        for path, items in per_file.items():
            full = os.path.join(FE, path.replace("/", os.sep))
            lines = io.open(full, encoding="utf-8", newline="").read().split("\n")
            # process from the bottom up so line numbers stay valid
            for line_no, _col, name in sorted(items, reverse=True):
                idx = line_no - 1
                if idx >= len(lines):
                    continue
                new_line = remove_name_from_import_line(lines[idx], name)
                if new_line is None:
                    # not an import line: maybe a bare `import "x";` or a local decl — skip
                    continue
                if new_line == "":
                    del lines[idx]
                else:
                    lines[idx] = new_line
            io.open(full, "w", encoding="utf-8", newline="").write("\n".join(lines))
    # final count
    stdout = run_tsc()
    t6133 = len(re.findall(r"error TS6133", stdout))
    total = len(re.findall(r"error TS\d+", stdout))
    print(f"after: TS6133={t6133}, total errors={total}")


if __name__ == "__main__":
    main()
