import { readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { formatIsoTimestamp, formatTimestampMs } from "../lib/messageTimestamp";

// tl1758 (C6) — single-source absolute-timestamp family. Pins the behaviour of
// the two lib exports that replaced five scattered private `formatTime`
// copies (dispatch / notifications / inbox / group panels: epoch-ms form;
// feedback panel: ISO-string form), plus a source-level pin that keeps the
// consolidation from drifting back (same pattern as tl1012 ③ / tl998 ③).
//
// Behaviour contract (zero visible change, except the declared edge):
//  - formatTimestampMs: falsy ms (0 / NaN) → "" (the guard three of the four
//    original copies already had; notifications' guardless copy rendered a
//    misleading 1970 epoch stamp and is the one declared edge convergence —
//    in practice NotifyArchiveEntry.at is always Date.now() at archive time,
//    so 0 is reachable only via corrupted persisted state);
//    any other value byte-identical to `new Date(ms).toLocaleString()`.
//  - formatIsoTimestamp: unparseable input echoes the original string back
//    (a malformed timestamp stays visible); parseable input byte-identical
//    to `date.toLocaleString()`.
// The `toLocaleString` comparisons below are self-relative (same runtime,
// same locale) so the assertions hold in any TZ/locale environment.

describe("formatTimestampMs", () => {
  it("returns '' for falsy inputs (0 / NaN) — the majority-form guard", () => {
    assert.equal(formatTimestampMs(0), "");
    assert.equal(formatTimestampMs(Number.NaN), "");
  });

  it("is byte-identical to toLocaleString for positive and negative ms", () => {
    const samples = [
      1,
      1_000,
      1_760_000_000_000, // ~2025-10
      1_795_000_000_000, // ~2026-02
      Date.now(),
      -1, // guard only blocks falsy — negative ms still renders (原实现同)
      86_400_000, // epoch + 1d
    ];
    for (const ms of samples) {
      assert.equal(formatTimestampMs(ms), new Date(ms).toLocaleString(), `ms=${ms}`);
    }
  });

  it("agrees with formatIsoTimestamp for the same instant (ms vs iso forms)", () => {
    const ms = 1_795_000_000_123;
    assert.equal(formatTimestampMs(ms), formatIsoTimestamp(new Date(ms).toISOString()));
  });
});

describe("formatIsoTimestamp", () => {
  it("is byte-identical to toLocaleString for parseable input", () => {
    const samples = [
      "2026-11-23T10:30:00Z",
      "2026-11-23T10:30:00.123Z",
      "2020-02-29T00:00:00Z", // leap day
      new Date().toISOString(),
    ];
    for (const iso of samples) {
      assert.equal(formatIsoTimestamp(iso), new Date(iso).toLocaleString(), `iso=${iso}`);
    }
  });

  it("echoes the original string back for unparseable input", () => {
    assert.equal(formatIsoTimestamp("not-a-date"), "not-a-date");
    assert.equal(formatIsoTimestamp(""), "");
  });
});

// ── tl1758 防复发门禁（源码级，全仓唯一定义源）────────────────────────────
//
// 验收口径：formatTime 全仓唯一定义源。规则（src/** 全量扫描，__tests__ 豁免）：
//   ① `function formatTime` 定义 = 0（任何文件不得再私抄该名——绝对时间戳族
//      用 formatTimestampMs，聊天气泡三档适配器用 ChannelMessageStream 内的
//      formatChatTimestamp，名字空间各归各）；
//   ② `const formatTime =` 绑定只允许两种逐字别名（指向 lib 两导出）——
//      防箭函数/裸表达式绕过 ① 溜回来；
//   ③ formatTimestampMs / formatIsoTimestamp 的 export function 定义全仓
//      只允许出现在 lib/messageTimestamp.ts（防 lib 间再分叉）。
// VersionSwitchDialog 的 `formatTime:` prop（App.tsx 注入 lambda）是依赖注入
// 不是定义，正则天然不命中；若未来要连 prop 面一起收口，另立任务申报。
describe("tl1758 consolidation pin (source-level, anti-drift)", () => {
  const here = dirname(fileURLToPath(import.meta.url));
  const srcRoot = resolve(here, "..", "..", "src");

  function walk(dir: string): string[] {
    const out: string[] = [];
    for (const entry of readdirSync(dir)) {
      const full = join(dir, entry);
      if (statSync(full).isDirectory()) {
        if (entry === "__tests__" || entry === "node_modules" || entry === "wailsjs") continue;
        out.push(...walk(full));
      } else if (/\.tsx?$/.test(entry)) {
        out.push(full);
      }
    }
    return out;
  }

  const files = walk(srcRoot);
  const rel = (f: string) => f.replace(/\\/g, "/").slice(srcRoot.replace(/\\/g, "/").length + 1);

  it("① no `function formatTime` definition anywhere under src", () => {
    const offenders = files
      .filter((f) => /^\s*function formatTime\s*\(/m.test(readFileSync(f, "utf8")))
      .map(rel);
    assert.deepEqual(offenders, [], `re-scattered formatTime definitions: ${offenders.join(", ")}`);
  });

  it("② `const formatTime =` bindings must be verbatim aliases of the two lib exports", () => {
    const allowed = /^\s*const formatTime\s*=\s*(?:formatTimestampMs|formatIsoTimestamp)\s*;\s*$/m;
    const offenders: string[] = [];
    for (const f of files) {
      const src = readFileSync(f, "utf8");
      for (const match of src.matchAll(/^\s*const formatTime\s*=.*$/gm)) {
        if (!allowed.test(match[0])) offenders.push(`${rel(f)}: ${match[0].trim()}`);
      }
    }
    assert.deepEqual(offenders, [], `non-alias formatTime bindings: ${offenders.join(" | ")}`);
  });

  it("③ formatTimestampMs / formatIsoTimestamp are exported only from lib/messageTimestamp.ts", () => {
    const libRel = "lib/messageTimestamp.ts";
    const offenders: string[] = [];
    for (const f of files) {
      if (rel(f) === libRel) continue;
      const src = readFileSync(f, "utf8");
      if (/export\s+(?:function|const)\s+format(?:TimestampMs|IsoTimestamp)\b/m.test(src)) {
        offenders.push(rel(f));
      }
    }
    assert.deepEqual(offenders, [], `timestamp family re-exported/duplicated in: ${offenders.join(", ")}`);
    const lib = readFileSync(resolve(srcRoot, libRel), "utf8");
    assert.ok(lib.includes("export function formatTimestampMs"), "lib export surface: formatTimestampMs");
    assert.ok(lib.includes("export function formatIsoTimestamp"), "lib export surface: formatIsoTimestamp");
  });

  it("the five consolidated panels still import from lib/messageTimestamp", () => {
    const consolidated = [
      "components/CollabDispatchPanel.tsx",
      "components/CollabNotificationsPanel.tsx",
      "components/CollabInboxPanel.tsx",
      "components/CollabGroupPanel.tsx",
      "components/FeedbackPanel.tsx",
    ];
    for (const r of consolidated) {
      const src = readFileSync(resolve(srcRoot, r), "utf8");
      assert.ok(src.includes("lib/messageTimestamp"), `${r} no longer imports from lib/messageTimestamp`);
    }
  });
});
